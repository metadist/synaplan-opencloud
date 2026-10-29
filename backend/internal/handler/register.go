package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// registrationAttempts bounds the startup announcement; later changes go
// through the normal request path.
const registrationAttempts = 6

// RegisterInBackground announces this OpenCloud to Synaplan on startup
// when an instance credential is held, with backoff while Synaplan is
// still coming up. It never registers: that is the administrator's click
// on the Synaplan page ({@see RegisterInstance}), or a credential the
// operator obtained and configured.
func (h *Handler) RegisterInBackground(ctx context.Context) {
	wait := 5 * time.Second
	for attempt := 1; attempt <= registrationAttempts; attempt++ {
		attemptCtx, cancel := bounded(ctx)
		done := h.announce(attemptCtx)
		cancel()
		if done || ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait *= 2
	}
	log.Printf("announce instance: giving up after %d attempts; Synaplan learns this app's capabilities at the next restart", registrationAttempts)
}

// instanceStatus is what the Synaplan page shows about this OpenCloud's
// registration: nothing yet, waiting for approval, or approved.
type instanceStatus struct {
	Registered bool   `json:"registered"`
	Status     string `json:"status"`
	Host       string `json:"host"`
}

// InstanceStatus reports the registration state. Everyone may read it;
// only an administrator can change it.
func (h *Handler) InstanceStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.instanceState(r.Context()))
}

func (h *Handler) instanceState(ctx context.Context) instanceStatus {
	state := instanceStatus{Host: h.publicHost()}
	if !h.hasInstance() {
		return state
	}
	state.Registered = true
	status, body, err := h.synaplanDo(ctx, http.MethodGet, "/api/v1/platform-links/instances/self", nil, callOptions{
		skipUser: true,
		extra:    h.instanceHeaders(),
	})
	if err != nil || status != http.StatusOK {
		// Synaplan refuses a pending instance's credential on most routes;
		// self answers for both, so anything else is an outage or a
		// revoked instance. Say "pending" rather than invent "active".
		state.Status = "pending"
		return state
	}
	var parsed struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(body, &parsed)
	state.Status = parsed.Status
	if state.Status == "" {
		state.Status = "pending"
	}
	return state
}

// RegisterInstance registers this OpenCloud with Synaplan. An OpenCloud
// administrator triggers it, and the call carries their own identity
// through the standard token exchange: when that person is a Synaplan
// administrator too, the instance is active at once; otherwise it is
// pending until a Synaplan administrator approves it. No key of any kind
// is involved; the credential Synaplan hands back is this instance's own
// and is stored here.
func (h *Handler) RegisterInstance(w http.ResponseWriter, r *http.Request) {
	if !h.isAdmin(r.Context()) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "Only an administrator can register this OpenCloud.", Code: "admin_only"})
		return
	}
	if h.hasInstance() {
		writeJSON(w, http.StatusOK, h.instanceState(r.Context()))
		return
	}
	status, body, err := h.synaplanCall(r.Context(), http.MethodPost, "/api/v1/platform-links/instances", map[string]any{
		"client":        "opencloud",
		"host":          h.publicHost(),
		"redirect_uris": []string{h.settings.PublicURL},
	})
	if err != nil {
		log.Printf("register instance: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.", Code: "synaplan_unreachable"})
		return
	}
	if status >= 400 {
		writeUpstream(w, status, body)
		return
	}
	var created struct {
		InstanceID     string `json:"instance_id"`
		InstanceSecret string `json:"instance_secret"`
		Status         string `json:"status"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.InstanceID == "" || created.InstanceSecret == "" {
		log.Printf("register instance: response had no credentials")
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan could not finish this. Try again in a minute, or ask your administrator.", Code: "synaplan_error"})
		return
	}
	h.setCredential(created.InstanceID, created.InstanceSecret)
	if err := h.store().SetInstance(created.InstanceID, created.InstanceSecret); err != nil {
		log.Printf("store instance: %v", err)
	}
	// A pending instance cannot announce yet; the startup path repeats
	// this once the instance is approved and the service restarts.
	h.announce(r.Context())
	writeJSON(w, http.StatusOK, instanceStatus{
		Registered: true,
		Status:     firstNonEmpty(created.Status, "pending"),
		Host:       h.publicHost(),
	})
}

// publicHost is the host part of the address people open, the name
// Synaplan lists this OpenCloud under.
func (h *Handler) publicHost() string {
	if parsed, err := url.Parse(h.settings.PublicURL); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return strings.TrimPrefix(strings.TrimPrefix(h.settings.PublicURL, "https://"), "http://")
}

// announce tells Synaplan what this app can do and where to call it. It
// reports whether there is nothing left to do: no instance to announce,
// or the announcement arrived.
func (h *Handler) announce(ctx context.Context) bool {
	if !h.hasInstance() {
		return true
	}
	body := map[string]any{
		"app_capabilities": []string{"deliveries", "pickups", "read_scope", "source_events", "watches"},
		"app_version":      "1.1.0",
		"oidc_client_id":   h.settings.OIDCClientID,
	}
	if h.settings.DeliveryURL != "" {
		body["delivery_url"] = h.settings.DeliveryURL
	}
	if h.settings.ServerID != "" {
		body["server_id"] = h.settings.ServerID
	}
	status, _, err := h.synaplanDo(ctx, http.MethodPatch, "/api/v1/platform-links/instances/self", body, callOptions{
		skipUser: true,
		extra:    h.instanceHeaders(),
	})
	if err != nil || status >= 400 {
		log.Printf("announce instance: status %d err %v", status, err)
		return false
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
