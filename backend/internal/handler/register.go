package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
)

// EnsureRegistered registers this OpenCloud with Synaplan when an admin
// key is configured and no instance id is stored yet, then tells Synaplan
// which features this app can do and where to call it.
func (h *Handler) EnsureRegistered(ctx context.Context) {
	if h == nil {
		return
	}
	if h.hasInstance() {
		h.announce(ctx)
		return
	}
	if h.settings.AdminAPIKey == "" {
		return
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
		return
	}
	var created struct {
		InstanceID     string `json:"instance_id"`
		InstanceSecret string `json:"instance_secret"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.InstanceID == "" || created.InstanceSecret == "" {
		log.Printf("register instance: response had no credentials")
		return
	}
	h.instanceID = created.InstanceID
	h.instanceSecret = created.InstanceSecret
	if err := h.store().SetInstance(created.InstanceID, created.InstanceSecret); err != nil {
		log.Printf("store instance: %v", err)
	}
	h.announce(ctx)
}

func (h *Handler) announce(ctx context.Context) {
	if !h.hasInstance() {
		return
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
	}
}
