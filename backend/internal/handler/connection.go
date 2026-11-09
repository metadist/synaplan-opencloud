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
		"background": h.canActAway(currentUserID(r.Context())),
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
	writeJSON(w, http.StatusOK, map[string]any{"bound": true})
}

// bindLogin tells Synaplan that this OpenCloud login is the person's
// Synaplan login. The person's own token proves it, and Synaplan takes the
// cloud from the token's client and the platform user from its subject.
func (h *Handler) bindLogin(ctx context.Context, uid string) (int, []byte, error) {
	status, body, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/sso/bind", map[string]string{
		"external_id": uid,
	}, callOptions{})
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
	status, body, err := h.synaplanDo(ctx, http.MethodGet, "/api/v1/platform-links/sso/status", nil, callOptions{})
	if err != nil || status != http.StatusOK {
		return false
	}
	var parsed struct {
		Bound bool `json:"bound"`
	}
	_ = json.Unmarshal(body, &parsed)
	return parsed.Bound
}
