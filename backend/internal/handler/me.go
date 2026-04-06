package handler

import (
	"fmt"
	"log"
	"net/http"
	"time"

	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"
)

type meResponse struct {
	Status       string `json:"status"`
	Timestamp    string `json:"timestamp"`
	SynaplanURL  string `json:"synaplanUrl"`
	UserID       string `json:"userId"`
	SynaplanResp string `json:"synaplanResponse,omitempty"`
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
