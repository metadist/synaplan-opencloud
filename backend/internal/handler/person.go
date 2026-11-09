package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/metadist/synaplan-opencloud/internal/delegation"
	"github.com/metadist/synaplan-opencloud/internal/synaplanauth"
)

// currentUserID is the signed-in person as Synaplan knows them: the subject
// of the identity provider's token, which the OpenCloud proxy checked. It
// is the key of the person's offline token here.
func currentUserID(ctx context.Context) string {
	return delegation.Subject(synaplanauth.OIDCTokenFromContext(ctx))
}

// DelegationStatus tells the browser whether a person can allow work while
// they are away, whether they did, and where the browser asks the identity
// provider for the code.
func (h *Handler) DelegationStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"available":         h.delegation.Configured(),
		"connected":         h.delegation.Connected(currentUserID(r.Context())),
		"clientId":          h.delegation.ClientID(),
		"authorizeEndpoint": h.delegation.AuthorizeEndpoint(),
	})
}

// DelegationConnect redeems the authorization code the browser obtained
// for this service's client. The offline token must belong to the
// signed-in person.
func (h *Handler) DelegationConnect(w http.ResponseWriter, r *http.Request) {
	if !h.delegation.Configured() {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "Working while you are away needs organisation login."})
		return
	}
	var req struct {
		Code        string `json:"code"`
		Verifier    string `json:"verifier"`
		RedirectURI string `json:"redirectUri"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil || req.Code == "" || req.Verifier == "" || req.RedirectURI == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "The sign-in answer is incomplete."})
		return
	}
	if err := h.delegation.Connect(r.Context(), req.Code, req.Verifier, req.RedirectURI, currentUserID(r.Context())); err != nil {
		log.Printf("delegation connect: %v", err)
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Synaplan could not be allowed to work for you. Try again."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true})
}

// DelegationDisconnect revokes the signed-in person's offline token.
func (h *Handler) DelegationDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := h.delegation.Disconnect(r.Context(), currentUserID(r.Context())); err != nil {
		log.Printf("delegation disconnect: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": false})
}

// personBearer is the Synaplan token for sub, minted from their offline
// token. It authenticates every call made while the person is away.
func (h *Handler) personBearer(ctx context.Context, sub string) (string, error) {
	if !h.delegation.Configured() {
		return "", errors.New("working while a person is away needs organisation login")
	}
	return h.delegation.Token(ctx, sub, h.audience)
}
