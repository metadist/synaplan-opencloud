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
	"net/url"
	"strings"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/state"
)

func (h *Handler) answerPickup(w http.ResponseWriter, ctx context.Context, id string) {
	result := h.runPickup(ctx, id)
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) runPickup(ctx context.Context, id string) map[string]any {
	status, body, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/pickups/"+url.PathEscape(id)+"/claim", map[string]any{}, callOptions{
		skipUser: true,
		extra:    h.instanceHeaders(),
	})
	if err != nil || status >= http.StatusInternalServerError {
		// Nothing was claimed. Let Synaplan's retry in.
		log.Printf("claim pickup %s: status %d err %v", id, status, err)
		h.releaseSeen("pickup", id)
		return map[string]any{"status": "pending"}
	}
	if status != http.StatusOK {
		return map[string]any{"status": "failed", "listed": 0, "sent": 0, "skipped": []any{}}
	}
	// The selector follows Synaplan's pickup contract: folder_id names the
	// folder, folder_path is only what the requester claimed.
	var claim struct {
		ExternalID string `json:"external_id"`
		Selector   struct {
			FolderID string `json:"folder_id"`
		} `json:"selector"`
	}
	if err := json.Unmarshal(body, &claim); err != nil || claim.ExternalID == "" {
		return h.finishPickup(ctx, id, "failed", 0, 0, nil)
	}
	folderID := claim.Selector.FolderID
	if h.cs3 == nil || h.settings.MachineAuthKey == "" || folderID == "" {
		return h.finishPickup(ctx, id, "failed", 0, 0, nil)
	}
	userCtx, err := h.cs3.Impersonate(ctx, claim.ExternalID)
	if err != nil {
		log.Printf("pickup impersonate: %v", err)
		return h.finishPickup(ctx, id, "failed", 0, 0, nil)
	}
	// The folder's real path comes from OpenCloud, never from the request:
	// a signature proves Synaplan asked, not that the id lives where the
	// selector says it does.
	meta, err := h.cs3.StatAny(userCtx, folderID)
	if err != nil {
		log.Printf("pickup stat: %v", err)
		return h.finishPickup(ctx, id, "failed", 0, 0, []map[string]string{{"id": folderID, "reason": "not_found"}})
	}
	folderPath := canonicalPath(meta.Path)
	// The person's setting as it is now, not as it was when the read was
	// queued. "Only what I watch" is checked against their current watches.
	user := h.store().User(claim.ExternalID)
	watched := h.activeWatchesFor(ctx, claim.ExternalID)
	if !scopeAllows(user.ReadScope, watched, folderID, folderPath) {
		return h.finishPickup(ctx, id, "failed", 0, 0, []map[string]string{{"id": folderID, "reason": "not_allowed"}})
	}
	children, err := h.cs3.List(userCtx, folderID)
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
			"folder": folderPath, "size": child.Size, "mime": child.MimeType,
		})
	}
	listStatus, listBody, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/pickups/"+url.PathEscape(id)+"/listing", map[string]any{"items": items}, callOptions{
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

// scopeAllows is the app side of "What Synaplan may read". A signed
// notification proves Synaplan asked, not that the person still allows
// it, so the current setting decides. "Only what I watch" allows exactly
// the folders the person has an active watch on; folders they named
// count in "allowed" mode as well.
func scopeAllows(scope state.ReadScope, watched []indexWatch, id, relPath string) bool {
	if id == "" {
		return false
	}
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
		return watchedFolder(watched, id)
	case "", "watched":
		return watchedFolder(watched, id)
	default:
		return false
	}
}

func watchedFolder(watched []indexWatch, id string) bool {
	for _, watch := range watched {
		if watch.TargetKind == "folder" && watch.TargetID == id {
			return true
		}
	}
	return false
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
	part, err := filePart(mw, child.Name, child.MimeType)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file.Body); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	status, _, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/pickups/"+url.PathEscape(pickupID)+"/files", nil, callOptions{
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
	_, _, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/pickups/"+url.PathEscape(id)+"/result", result, callOptions{
		skipUser: true,
		extra:    h.instanceHeaders(),
	})
	if err != nil {
		log.Printf("pickup result %s: %v", id, err)
	}
	return result
}
