package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"

	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
	"github.com/metadist/synaplan-opencloud/internal/synaplanauth"
)

// Handler handles Synaplan API requests.
//
// All outgoing Synaplan API calls happen through synaplanAPI, which
// has a synaplanauth request editor registered — per-user token
// exchange is applied transparently on every call.
type Handler struct {
	synaplanURL string
	synaplanAPI *synaplanapi.ClientWithResponses
}

// New creates a new Handler. The exchanger is wired into the generated
// Synaplan API client so every outgoing call is authenticated on
// behalf of the end-user whose request triggered it.
func New(exchanger synaplanauth.TokenExchanger, synaplanURL string) (*Handler, error) {
	apiClient, err := synaplanapi.NewClientWithResponses(
		synaplanURL,
		synaplanapi.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}),
		synaplanapi.WithRequestEditorFn(synaplanauth.NewRequestEditor(exchanger)),
	)
	if err != nil {
		return nil, fmt.Errorf("creating synaplan API client: %w", err)
	}
	return &Handler{
		synaplanURL: synaplanURL,
		synaplanAPI: apiClient,
	}, nil
}

type meResponse struct {
	Status       string `json:"status"`
	Timestamp    string `json:"timestamp"`
	SynaplanURL  string `json:"synaplan_url"`
	UserID       string `json:"user_id"`
	SynaplanResp string `json:"synaplan_response,omitempty"`
	Error        string `json:"error,omitempty"`
}

// Me tests the full per-user auth flow by calling Synaplan's
// /api/v1/auth/me via the generated client. The client's registered
// request editor handles OIDC → Synaplan token exchange transparently.
// synaplanauth.Middleware guarantees a reva user is in the context.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	userID := revactx.ContextMustGetUser(r.Context()).GetId().GetOpaqueId()

	resp, err := h.synaplanAPI.GetApiAuthMeWithResponse(r.Context())
	if err != nil {
		log.Printf("synaplan /me failed for user %s: %v", userID, err)
		writeJSON(w, http.StatusBadGateway, meResponse{
			Timestamp:   now(),
			Status:      "error",
			SynaplanURL: h.synaplanURL,
			UserID:      userID,
			Error:       fmt.Sprintf("synaplan request failed: %v", err),
		})
		return
	}

	writeJSON(w, http.StatusOK, meResponse{
		Timestamp:    now(),
		Status:       "ok",
		SynaplanURL:  h.synaplanURL,
		UserID:       userID,
		SynaplanResp: string(resp.Body),
	})
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("failed to encode response: %v", err)
	}
}
