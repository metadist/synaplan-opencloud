package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/notify"
)

// Inbound is the address Synaplan calls. Nothing is fetched until the
// notification verifies: signature, this instance, five minutes, and an
// id not seen in the last ten minutes. An id is released again when the
// work behind it could not start, so Synaplan's retry is handled.
func (h *Handler) Inbound(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": notify.BadSignature})
		return
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": notify.BadSignature})
		return
	}
	if !h.hasInstance() {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": notify.BadSignature})
		return
	}
	instanceID, _ := h.instance()
	keys := h.notifyKeys(r.Context())
	reason := notify.Verify(body, instanceID, keys, time.Now().Unix())
	if reason != notify.OK {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": reason})
		return
	}
	kind, _ := body["type"].(string)
	id := notificationID(body["id"])
	if kind == "" || id == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": notify.BadSignature})
		return
	}
	if h.markSeen(kind, id) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": notify.Replay})
		return
	}
	if h.settings.HoldInbound {
		writeJSON(w, http.StatusAccepted, map[string]string{"state": "pending"})
		return
	}
	ctx, cancel := bounded(r.Context())
	defer cancel()
	switch kind {
	case "delivery":
		h.deliverCopy(w, ctx, id)
	case "pickup":
		h.answerPickup(w, ctx, id)
	case "identity_revoked":
		h.revokeIdentity(id)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) markSeen(kind, id string) bool {
	h.seenMu.Lock()
	defer h.seenMu.Unlock()
	if h.seen == nil {
		h.seen = notify.NewSeen()
	}
	return h.seen.AlreadySeen(kind, id, time.Now().Unix())
}

// releaseSeen lets Synaplan's next retry of the same notification through.
func (h *Handler) releaseSeen(kind, id string) {
	h.seenMu.Lock()
	defer h.seenMu.Unlock()
	h.seen.Forget(kind, id)
}

func notificationID(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatInt(int64(t), 10)
	default:
		return ""
	}
}

// revokeIdentity handles identity_revoked. Synaplan names the identity
// row it revoked; the person is found through the link id stored at bind.
func (h *Handler) revokeIdentity(id string) {
	linkID, err := strconv.Atoi(id)
	if err != nil {
		return
	}
	uid, ok := h.store().UserByLinkID(linkID)
	if !ok {
		return
	}
	if err := h.store().ForgetUser(uid); err != nil {
		log.Printf("identity revoked: forget %s: %v", uid, err)
	}
}

// notifyKeys returns Synaplan's current notify public keys. A 200 answer
// is authoritative, an empty list included: a key Synaplan dropped must
// stop being trusted. Only a failed or unreadable answer keeps the cache.
func (h *Handler) notifyKeys(ctx context.Context) []string {
	h.keysMu.Lock()
	defer h.keysMu.Unlock()
	if time.Since(h.keysAt) < 10*time.Minute && h.keysLoaded {
		return h.keys
	}
	status, body, err := h.synaplanDo(ctx, http.MethodGet, "/api/v1/platform-links/instances/self", nil, callOptions{
		skipUser: true,
		extra:    h.instanceHeaders(),
	})
	if err != nil || status != http.StatusOK {
		log.Printf("notify keys: status %d err %v", status, err)
		return h.keys
	}
	var parsed struct {
		NotifyKeys []struct {
			PublicKey string `json:"publicKey"`
		} `json:"notify_keys"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return h.keys
	}
	keys := make([]string, 0, len(parsed.NotifyKeys))
	for _, key := range parsed.NotifyKeys {
		if key.PublicKey != "" {
			keys = append(keys, key.PublicKey)
		}
	}
	h.keys = keys
	h.keysLoaded = true
	h.keysAt = time.Now()
	return h.keys
}

func (h *Handler) deliverCopy(w http.ResponseWriter, ctx context.Context, id string) {
	status, body, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/deliveries/"+url.PathEscape(id)+"/claim", map[string]any{}, callOptions{
		skipUser: true,
		extra:    h.instanceHeaders(),
	})
	if err != nil || status >= http.StatusInternalServerError {
		// Nothing was claimed. Let the retry in.
		log.Printf("claim delivery %s: status %d err %v", id, status, err)
		h.releaseSeen("delivery", id)
		writeJSON(w, http.StatusAccepted, map[string]string{"state": "pending"})
		return
	}
	if status != http.StatusOK {
		// Another worker has it, or it expired. Final for this id.
		writeJSON(w, http.StatusAccepted, map[string]string{"state": "pending"})
		return
	}
	var claim struct {
		ExternalID   string `json:"external_id"`
		Name         string `json:"name"`
		DownloadPath string `json:"download_path"`
		Target       struct {
			Kind string `json:"kind"`
			Ref  string `json:"ref"`
		} `json:"target"`
	}
	if err := json.Unmarshal(body, &claim); err != nil || claim.ExternalID == "" || !strings.HasPrefix(claim.DownloadPath, "/api/v1/platform-links/deliveries/") {
		h.reportDelivery(ctx, id, map[string]string{"status": "failed", "error_code": "unreachable"})
		writeJSON(w, http.StatusOK, map[string]string{"status": "failed", "error_code": "unreachable"})
		return
	}
	result := h.writeClaim(ctx, claim.ExternalID, claim.Name, claim.DownloadPath, claim.Target.Kind, claim.Target.Ref)
	h.reportDelivery(ctx, id, result)
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) writeClaim(ctx context.Context, uid, name, downloadPath, kind, ref string) map[string]string {
	if h.cs3 == nil || h.settings.MachineAuthKey == "" {
		return map[string]string{"status": "failed", "error_code": "unreachable"}
	}
	userCtx, err := h.cs3.Impersonate(ctx, uid)
	if err != nil {
		log.Printf("impersonate %s: %v", uid, err)
		return map[string]string{"status": "failed", "error_code": "not_found"}
	}
	status, body, err := h.synaplanDo(userCtx, http.MethodGet, downloadPath, nil, callOptions{
		skipUser: true,
		extra:    h.instanceHeaders(),
		limit:    maxCopyBytes,
	})
	if err != nil || status != http.StatusOK {
		log.Printf("download delivery: status %d err %v", status, err)
		if errors.Is(err, errTooLarge) {
			return map[string]string{"status": "failed", "error_code": "too_large"}
		}
		return map[string]string{"status": "failed", "error_code": "unreachable"}
	}
	if name == "" {
		name = "file"
	}
	user := h.store().User(uid)
	var written cs3reader.Written
	switch {
	case user.CopyFolder != "":
		written, err = h.cs3.SaveToFolder(userCtx, user.CopyFolder, name, body)
	case (kind == "folder_id" || kind == "next_to") && ref != "":
		written, err = h.cs3.SaveChild(userCtx, ref, name, body)
	default:
		written, err = h.cs3.Save(userCtx, name, body)
	}
	if err != nil {
		log.Printf("write delivery: %v", err)
		return map[string]string{"status": "failed", "error_code": "unreachable"}
	}
	h.store().RememberOwnWrite(uid, written.ID, time.Now().Unix())
	return map[string]string{
		"status":      "delivered",
		"remote_path": "/" + strings.TrimLeft(written.Path, "/"),
		"remote_id":   written.ID,
		"open_url":    h.openURL(written.ID),
	}
}

// openURL is OpenCloud's private link for a resource id.
func (h *Handler) openURL(resourceID string) string {
	base := strings.TrimRight(h.settings.PublicURL, "/")
	if base == "" || resourceID == "" {
		return ""
	}
	return base + "/f/" + resourceID
}

func (h *Handler) reportDelivery(ctx context.Context, id string, result map[string]string) {
	_, _, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/deliveries/"+url.PathEscape(id)+"/result", result, callOptions{
		skipUser: true,
		extra:    h.instanceHeaders(),
	})
	if err != nil {
		log.Printf("report delivery %s: %v", id, err)
	}
}

// maxCopyBytes is the largest file this service writes from Synaplan.
const maxCopyBytes = 100 << 20
