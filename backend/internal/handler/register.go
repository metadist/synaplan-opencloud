package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"time"
)

// registrationAttempts is how often startup registration is retried
// before it gives up until the next restart. The waits grow from 5 s.
const registrationAttempts = 6

// RegisterInBackground registers and announces this OpenCloud without
// holding up the HTTP listener. Each attempt has its own deadline, so a
// Synaplan that accepts a connection and never answers costs one wait,
// not the service's health endpoint.
func (h *Handler) RegisterInBackground(ctx context.Context) {
	wait := 5 * time.Second
	for attempt := 1; attempt <= registrationAttempts; attempt++ {
		attemptCtx, cancel := bounded(ctx)
		done := h.ensureRegistered(attemptCtx)
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
	log.Printf("register instance: giving up after %d attempts; copies, folder reads and watches stay off until a restart", registrationAttempts)
}

// ensureRegistered registers this OpenCloud with Synaplan when an admin
// key is configured and no instance id is stored yet, then tells Synaplan
// which features this app can do and where to call it. It reports whether
// there is nothing left to do.
func (h *Handler) ensureRegistered(ctx context.Context) bool {
	if h == nil {
		return true
	}
	if h.hasInstance() {
		return h.announce(ctx)
	}
	if h.settings.AdminAPIKey == "" {
		return true
	}
	host := h.settings.PublicURL
	if parsed, err := url.Parse(h.settings.PublicURL); err == nil && parsed.Host != "" {
		host = parsed.Host
	}
	status, body, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/instances", map[string]any{
		"client":        "opencloud",
		"host":          host,
		"redirect_uris": []string{h.settings.PublicURL},
	}, callOptions{apiKey: h.settings.AdminAPIKey})
	if err != nil || status >= 400 {
		log.Printf("register instance: status %d err %v", status, err)
		return false
	}
	var created struct {
		InstanceID     string `json:"instance_id"`
		InstanceSecret string `json:"instance_secret"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.InstanceID == "" || created.InstanceSecret == "" {
		log.Printf("register instance: response had no credentials")
		return true
	}
	h.setCredential(created.InstanceID, created.InstanceSecret)
	if err := h.store().SetInstance(created.InstanceID, created.InstanceSecret); err != nil {
		log.Printf("store instance: %v", err)
	}
	return h.announce(ctx)
}

// announce tells Synaplan what this app can do and where to call it.
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
