package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"
)

type meResponse struct {
	Status       string           `json:"status"`
	Timestamp    string           `json:"timestamp"`
	SynaplanURL  string           `json:"synaplanUrl"`
	UserID       string           `json:"userId"`
	Account      *synaplanAccount `json:"account,omitempty"`
	SynaplanResp string           `json:"synaplanResponse,omitempty"`
	Error        string           `json:"error,omitempty"`
}

// synaplanAccount is the person the OIDC token exchange resolved to.
// OpenCloud does not ask for a second password: this is that login.
type synaplanAccount struct {
	Email     string `json:"email,omitempty"`
	FirstName string `json:"firstName,omitempty"`
	Level     string `json:"level,omitempty"`
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

	account := parseSynaplanAccount(resp.Body)
	status := "ok"
	if resp.StatusCode() != http.StatusOK || account == nil {
		status = "error"
	}
	writeJSON(w, http.StatusOK, meResponse{
		Timestamp:    now(),
		Status:       status,
		SynaplanURL:  h.synaplanURL,
		UserID:       userID,
		Account:      account,
		SynaplanResp: string(resp.Body),
	})
}

func parseSynaplanAccount(body []byte) *synaplanAccount {
	var parsed struct {
		User struct {
			Email     string `json:"email"`
			FirstName string `json:"firstName"`
			Level     string `json:"level"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.User.Email == "" {
		return nil
	}
	return &synaplanAccount{
		Email:     parsed.User.Email,
		FirstName: parsed.User.FirstName,
		Level:     parsed.User.Level,
	}
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}
