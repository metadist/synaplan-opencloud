package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
)

// Connection says whether this OpenCloud login is bound to Synaplan.
func (h *Handler) Connection(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":       "organisation",
		"configured": h.hasInstance(),
		"bound":      h.bound(r.Context()),
		"serverUrl":  h.synaplanURL,
		"publicUrl":  h.settings.PublicURL,
	})
}

// Bind connects this OpenCloud login to the person's Synaplan account.
func (h *Handler) Bind(w http.ResponseWriter, r *http.Request) {
	if !h.hasInstance() {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "Ask your administrator to connect this OpenCloud to Synaplan.", Code: "not_connected"})
		return
	}
	uid := currentUserID(r.Context())
	status, body, err := h.bindLogin(r.Context(), uid)
	if err != nil {
		log.Printf("sso bind: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.", Code: "synaplan_unreachable"})
		return
	}
	if status >= 400 {
		writeUpstream(w, status, body)
		return
	}
	h.ensureBackgroundKey(r.Context(), uid)
	writeJSON(w, http.StatusOK, map[string]any{"bound": true})
}

// bindLogin tells Synaplan that this OpenCloud login is the person's
// Synaplan login. The person's own token proves it, so the first cloud
// action (a watch, keep up to date) may do it without a separate click.
func (h *Handler) bindLogin(ctx context.Context, uid string) (int, []byte, error) {
	status, body, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/sso/bind", map[string]string{
		"external_id": uid,
	}, callOptions{extra: h.instanceHeaders()})
	if err != nil || status >= 400 {
		return status, body, err
	}
	var bound struct {
		LinkID int `json:"link_id"`
	}
	_ = json.Unmarshal(body, &bound)
	if bound.LinkID > 0 {
		user := h.store().User(uid)
		user.LinkID = bound.LinkID
		if err := h.store().PutUser(uid, user); err != nil {
			log.Printf("store link id: %v", err)
		}
	}
	return status, body, nil
}

// linked makes sure the person's login is bound before a call that needs
// the identity row. A failed bind is left to the call itself to report.
func (h *Handler) linked(ctx context.Context, uid string) {
	if uid == "" || !h.hasInstance() || h.store().User(uid).LinkID > 0 {
		return
	}
	if h.bound(ctx) {
		return
	}
	if _, _, err := h.bindLogin(ctx, uid); err != nil {
		log.Printf("sso bind before cloud action: %v", err)
	}
}

func (h *Handler) bound(ctx context.Context) bool {
	if !h.hasInstance() {
		return false
	}
	status, body, err := h.synaplanDo(ctx, http.MethodGet, "/api/v1/platform-links/sso/status?external_id="+currentUserID(ctx), nil, callOptions{extra: h.instanceHeaders()})
	if err != nil || status != http.StatusOK {
		return false
	}
	var parsed struct {
		Bound bool `json:"bound"`
	}
	_ = json.Unmarshal(body, &parsed)
	return parsed.Bound
}

// ensureBackgroundKey makes sure this service holds the key that works
// while the person is away. It reports whether one is held afterwards.
// Synaplan hands the plaintext out once; a later mint without it means
// another instance of this service has it, and that is reported as false.
func (h *Handler) ensureBackgroundKey(ctx context.Context, uid string) bool {
	if uid == "" || !h.hasInstance() {
		return false
	}
	if h.store().User(uid).BackgroundKey != "" {
		return true
	}
	h.linked(ctx, uid)
	status, body, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/sso/background-key", map[string]string{
		"external_id": uid,
	}, callOptions{extra: h.instanceHeaders()})
	if err != nil || status != http.StatusOK {
		log.Printf("background key: status %d err %v", status, err)
		return false
	}
	var parsed struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Key == "" {
		return false
	}
	user := h.store().User(uid)
	user.BackgroundKey = parsed.Key
	if err := h.store().PutUser(uid, user); err != nil {
		log.Printf("store background key: %v", err)
		return false
	}
	return true
}
