package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/state"
)

func (h *Handler) answerPickup(w http.ResponseWriter, ctx context.Context, id string) {
	result := h.runPickup(ctx, id)
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) runPickup(ctx context.Context, id string) map[string]any {
	status, body, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/pickups/"+id+"/claim", map[string]any{}, callOptions{
		skipUser: true,
		extra:    h.instanceHeaders(),
	})
	if err != nil || status != http.StatusOK {
		log.Printf("claim pickup %s: status %d err %v", id, status, err)
		return map[string]any{"status": "failed", "listed": 0, "sent": 0, "skipped": []any{}}
	}
	var claim struct {
		ExternalID string `json:"external_id"`
		Selector   struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"selector"`
	}
	if err := json.Unmarshal(body, &claim); err != nil || claim.ExternalID == "" {
		return h.finishPickup(ctx, id, "failed", 0, 0, nil)
	}
	user := h.store().User(claim.ExternalID)
	if !scopeAllows(user.ReadScope, claim.Selector.ID, claim.Selector.Path) {
		return h.finishPickup(ctx, id, "failed", 0, 0, []map[string]string{{"id": claim.Selector.ID, "reason": "not_allowed"}})
	}
	if h.cs3 == nil || h.settings.MachineAuthKey == "" || claim.Selector.ID == "" {
		return h.finishPickup(ctx, id, "failed", 0, 0, nil)
	}
	userCtx, err := h.cs3.Impersonate(ctx, claim.ExternalID)
	if err != nil {
		log.Printf("pickup impersonate: %v", err)
		return h.finishPickup(ctx, id, "failed", 0, 0, nil)
	}
	children, err := h.cs3.List(userCtx, claim.Selector.ID)
	if err != nil {
		log.Printf("pickup list: %v", err)
		return h.finishPickup(ctx, id, "failed", 0, 0, nil)
	}
	items := make([]map[string]any, 0, len(children))
	byID := map[string]cs3reader.Child{}
	for _, child := range children {
		if child.IsFolder || child.ID == "" {
			continue
		}
		byID[child.ID] = child
		items = append(items, map[string]any{
			"id": child.ID, "version": child.Etag, "name": child.Name,
			"folder": claim.Selector.Path, "size": child.Size, "mime": child.MimeType,
		})
	}
	listStatus, listBody, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/pickups/"+id+"/listing", map[string]any{"items": items}, callOptions{
		skipUser: true,
		extra:    h.instanceHeaders(),
	})
	if err != nil || listStatus != http.StatusOK {
		log.Printf("pickup listing: status %d err %v", listStatus, err)
		return h.finishPickup(ctx, id, "failed", len(items), 0, nil)
	}
	var decision struct {
		Upload []string `json:"upload"`
	}
	_ = json.Unmarshal(listBody, &decision)
	sent := 0
	var skipped []map[string]string
	for _, fileID := range decision.Upload {
		child, ok := byID[fileID]
		if !ok {
			skipped = append(skipped, map[string]string{"id": fileID, "reason": "not_found"})
			continue
		}
		if err := h.uploadPickupFile(userCtx, id, child); err != nil {
			log.Printf("pickup upload %s: %v", child.Name, err)
			skipped = append(skipped, map[string]string{"id": fileID, "reason": "unreachable"})
			continue
		}
		sent++
	}
	return h.finishPickup(ctx, id, "done", len(items), sent, skipped)
}

func scopeAllows(scope state.ReadScope, id, relPath string) bool {
	switch scope.Mode {
	case "all":
		return true
	case "allowed":
		for _, folder := range scope.Folders {
			if folder.ID != "" && folder.ID == id {
				return true
			}
			if folder.Path != "" && (folder.Path == relPath || strings.HasPrefix(relPath, folder.Path+"/")) {
				return true
			}
		}
		return false
	default:
		// "watched" is enforced by Synaplan before it asks. A pickup that
		// arrived is for a folder the person watches, or we would not list it
		// when they named nothing.
		return scope.Mode == "" || scope.Mode == "watched"
	}
}

func (h *Handler) uploadPickupFile(ctx context.Context, pickupID string, child cs3reader.Child) error {
	file, err := h.cs3.Open(ctx, child.ID)
	if err != nil {
		return err
	}
	defer func() { _ = file.Body.Close() }()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("id", child.ID)
	_ = mw.WriteField("version", child.Etag)
	part, err := mw.CreateFormFile("file", child.Name)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file.Body); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	status, _, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/pickups/"+pickupID+"/files", nil, callOptions{
		skipUser:    true,
		extra:       h.instanceHeaders(),
		raw:         bytes.NewReader(buf.Bytes()),
		contentType: mw.FormDataContentType(),
		limit:       1 << 20,
	})
	if err != nil {
		return err
	}
	if status >= 400 {
		return fmt.Errorf("pickup file status %d", status)
	}
	return nil
}

func (h *Handler) finishPickup(ctx context.Context, id, statusText string, listed, sent int, skipped []map[string]string) map[string]any {
	if skipped == nil {
		skipped = []map[string]string{}
	}
	result := map[string]any{
		"status": statusText, "listed": listed, "sent": sent, "skipped": skipped,
	}
	_, _, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/pickups/"+id+"/result", result, callOptions{
		skipUser: true,
		extra:    h.instanceHeaders(),
	})
	if err != nil {
		log.Printf("pickup result %s: %v", id, err)
	}
	return result
}

// RunReconciler keeps watches and searchable files moving while nobody
// has the folder open. It does nothing until this OpenCloud is registered
// and machine auth is configured. OpenCloud does not deliver file events
// to an extension, so this pass is the way changes are noticed.
func (h *Handler) RunReconciler(ctx context.Context) {
	if h == nil || !h.hasInstance() || h.settings.MachineAuthKey == "" {
		log.Printf("reconciler: not started (instance or machine auth missing)")
		return
	}
	h.reconcileOnce(ctx)
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.reconcileOnce(ctx)
		}
	}
}

func (h *Handler) reconcileOnce(ctx context.Context) {
	h.syncSourceEvents(ctx)
	h.syncWatches(ctx)
}

// WatchReconcileNow runs one pass for the signed-in person.
func (h *Handler) WatchReconcileNow(w http.ResponseWriter, r *http.Request) {
	if !h.hasInstance() {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "Ask your administrator to connect this OpenCloud to Synaplan.", Code: "not_connected"})
		return
	}
	h.reconcileOnce(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"sentence": "Synaplan checked your watched files."})
}
