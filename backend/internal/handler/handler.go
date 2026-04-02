package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"
)

// TokenExchanger defines the interface for token exchange operations.
type TokenExchanger interface {
	Exchange(userID, subjectToken string) (string, error)
}

// Handler handles Synaplan API requests.
type Handler struct {
	exchanger   TokenExchanger
	synaplanURL string
	httpClient  *http.Client
}

// New creates a new Handler.
func New(exchanger TokenExchanger, synaplanURL string) *Handler {
	return &Handler{
		exchanger:   exchanger,
		synaplanURL: synaplanURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type meResponse struct {
	Status       string `json:"status"`
	Timestamp    string `json:"timestamp"`
	SynaplanURL  string `json:"synaplan_url"`
	UserID       string `json:"user_id"`
	TokenOK      bool   `json:"token_ok"`
	SynaplanResp string `json:"synaplan_response,omitempty"`
	Error        string `json:"error,omitempty"`
}

// Me tests the full token exchange flow:
// 1. Extracts user identity from OpenCloud context
// 2. Exchanges the user's token for a Synaplan-scoped token
// 3. Calls Synaplan's /api/v1/auth/me with the exchanged token
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	u, ok := revactx.ContextGetUser(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, meResponse{
			Timestamp: now(),
			Status:    "error",
			Error:     "unauthorized: no user in context",
		})
		return
	}

	userID := u.GetId().GetOpaqueId()

	// The original OIDC access token (from Keycloak) arrives in the Authorization
	// header. The proxy does NOT strip it — it only adds x-access-token (reva JWT).
	// We need the OIDC token for token exchange, not the reva JWT.
	oidcToken := extractBearerToken(r)
	if oidcToken == "" {
		writeJSON(w, http.StatusUnauthorized, meResponse{
			Timestamp: now(),
			Status:    "error",
			UserID:    userID,
			Error:     "no OIDC bearer token in Authorization header",
		})
		return
	}

	// Step 1: Exchange OIDC token for a Synaplan-scoped token
	synaplanToken, err := h.exchanger.Exchange(userID, oidcToken)
	if err != nil {
		log.Printf("token exchange failed for user %s: %v", userID, err)
		writeJSON(w, http.StatusBadGateway, meResponse{
			Timestamp:   now(),
			Status:      "error",
			SynaplanURL: h.synaplanURL,
			UserID:      userID,
			TokenOK:     false,
			Error:       fmt.Sprintf("token exchange failed: %v", err),
		})
		return
	}

	// Step 2: Call Synaplan /api/v1/auth/me with exchanged token (verifies auth works)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, h.synaplanURL+"/api/v1/auth/me", nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, meResponse{
			Timestamp: now(),
			Status:    "error",
			Error:     fmt.Sprintf("creating request: %v", err),
		})
		return
	}
	req.Header.Set("Authorization", "Bearer "+synaplanToken)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, meResponse{
			Timestamp:   now(),
			Status:      "error",
			SynaplanURL: h.synaplanURL,
			UserID:      userID,
			TokenOK:     true,
			Error:       fmt.Sprintf("synaplan request failed: %v", err),
		})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	writeJSON(w, http.StatusOK, meResponse{
		Timestamp:    now(),
		Status:       "ok",
		SynaplanURL:  h.synaplanURL,
		UserID:       userID,
		TokenOK:      true,
		SynaplanResp: string(body),
	})
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// extractBearerToken extracts the Bearer token from the Authorization header.
func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("failed to encode response: %v", err)
	}
}
