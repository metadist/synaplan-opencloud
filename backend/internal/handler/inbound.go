package handler

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/metadist/synaplan-opencloud/internal/notify"
)

// Inbound is the address Synaplan calls. Nothing is fetched until the
// notification verifies: signature, this instance, five minutes, and an
// id not seen in the last ten minutes.
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
	keys := h.notifyKeys(r.Context())
	reason := notify.Verify(body, h.instanceID, keys, time.Now().Unix())
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
	h.seenMu.Lock()
	replay := h.seen.AlreadySeen(kind, id, time.Now().Unix())
	h.seenMu.Unlock()
	if replay {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": notify.Replay})
		return
	}
	if h.settings.HoldInbound {
		writeJSON(w, http.StatusAccepted, map[string]string{"state": "pending"})
		return
	}
	switch kind {
	case "delivery":
		h.deliverCopy(w, r.Context(), id)
	case "pickup":
		h.answerPickup(w, r.Context(), id)
	case "identity_revoked":
		_ = h.store().ForgetUser(id)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
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

func (h *Handler) notifyKeys(ctx context.Context) []string {
	h.keysMu.Lock()
	defer h.keysMu.Unlock()
	if time.Since(h.keysAt) < 10*time.Minute && len(h.keys) > 0 {
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
	if len(keys) > 0 {
		h.keys = keys
		h.keysAt = time.Now()
	}
	return h.keys
}

func (h *Handler) deliverCopy(w http.ResponseWriter, ctx context.Context, id string) {
	status, body, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/deliveries/"+id+"/claim", map[string]any{}, callOptions{
		skipUser: true,
		extra:    h.instanceHeaders(),
	})
	if err != nil || status != http.StatusOK {
		log.Printf("claim delivery %s: status %d err %v", id, status, err)
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
	if err := json.Unmarshal(body, &claim); err != nil || claim.ExternalID == "" || claim.DownloadPath == "" {
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
		limit:    100 << 20,
	})
	if err != nil || status != http.StatusOK {
		log.Printf("download delivery: status %d err %v", status, err)
		return map[string]string{"status": "failed", "error_code": "unreachable"}
	}
	if name == "" {
		name = "file"
	}
	user := h.store().User(uid)
	var saved string
	switch {
	case user.CopyFolder != "":
		saved, err = h.cs3.SaveToFolder(userCtx, user.CopyFolder, name, body)
	case (kind == "folder_id" || kind == "next_to") && ref != "":
		saved, err = h.cs3.SaveChild(userCtx, ref, name, body)
	default:
		saved, err = h.cs3.Save(userCtx, name, body)
	}
	if err != nil {
		log.Printf("write delivery: %v", err)
		return map[string]string{"status": "failed", "error_code": "unreachable"}
	}
	h.store().RememberOwnWrite(uid, saved, time.Now().Unix())
	open := strings.TrimRight(h.settings.PublicURL, "/") + "/f/" + saved
	return map[string]string{
		"status":      "delivered",
		"remote_path": "/" + strings.TrimLeft(saved, "/"),
		"remote_id":   saved,
		"open_url":    open,
	}
}

func (h *Handler) reportDelivery(ctx context.Context, id string, result map[string]string) {
	_, _, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/deliveries/"+id+"/result", result, callOptions{
		skipUser: true,
		extra:    h.instanceHeaders(),
	})
	if err != nil {
		log.Printf("report delivery %s: %v", id, err)
	}
}
