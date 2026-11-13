package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"

	"github.com/metadist/synaplan-opencloud/internal/delegation"
	"github.com/metadist/synaplan-opencloud/internal/synaplanauth"
)

// errNoDelegation is returned when this service runs without organisation
// login and so cannot act for anyone while they are away.
var errNoDelegation = errors.New("working while a person is away needs organisation login")

// signedInSubject is the signed-in person as the identity provider knows
// them, the key of their offline token here and the subject Synaplan names
// in its notifications. It is read from the token the exchange returns,
// never from the incoming bearer: the identity provider refuses a forged
// bearer, and the exchange caches by the reva user OpenCloud verified.
func (h *Handler) signedInSubject(ctx context.Context) (string, error) {
	if h.exchanger == nil {
		return "", errNoDelegation
	}
	user, ok := revactx.ContextGetUser(ctx)
	if !ok {
		return "", errors.New("no reva user in request context")
	}
	token, err := h.exchanger.Exchange(user.GetId().GetOpaqueId(), synaplanauth.OIDCTokenFromContext(ctx))
	if err != nil {
		return "", err
	}
	sub := delegation.Subject(token)
	if sub == "" {
		return "", errors.New("the exchanged token names no subject")
	}
	return sub, nil
}

// DelegationStatus tells the browser whether the signed-in person can allow
// work while they are away, whether they did, and where the browser asks
// the identity provider for the code.
func (h *Handler) DelegationStatus(w http.ResponseWriter, r *http.Request) {
	if !h.delegation.Configured() {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "connected": false})
		return
	}
	sub, err := h.signedInSubject(r.Context())
	if err != nil {
		log.Printf("delegation status: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "Synaplan could not check who you are. Try again.",
			Code:  "identity_check_failed",
		})
		return
	}
	clientID, endpoint, err := h.delegation.Authorization(r.Context())
	if err != nil {
		log.Printf("delegation status: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "The sign-in service cannot be reached. Try again later.",
			Code:  "idp_unreachable",
		})
		return
	}
	connected, err := h.delegation.Connected(r.Context(), sub)
	if err != nil {
		log.Printf("delegation status: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "Synaplan cannot check this permission right now. Try again later.",
			Code:  "permission_unavailable",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available":         true,
		"connected":         connected,
		"clientId":          clientID,
		"authorizeEndpoint": endpoint,
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
	sub, err := h.signedInSubject(r.Context())
	if err == nil {
		err = h.delegation.Connect(r.Context(), req.Code, req.Verifier, req.RedirectURI, sub)
	}
	if err != nil {
		log.Printf("delegation connect: %v", err)
		if writeStoreFailure(w, err) {
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Synaplan could not be allowed to work for you. Try again."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true})
}

// DelegationDisconnect forgets the signed-in person's offline token and
// revokes it at the identity provider. If the identity provider could not
// revoke it, the person is told: their session there may live on.
func (h *Handler) DelegationDisconnect(w http.ResponseWriter, r *http.Request) {
	if !h.delegation.Configured() {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "Working while you are away needs organisation login."})
		return
	}
	sub, err := h.signedInSubject(r.Context())
	if err != nil {
		log.Printf("delegation disconnect: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "Synaplan could not check who you are. Try again.",
			Code:  "identity_check_failed",
		})
		return
	}
	if err := h.delegation.Disconnect(r.Context(), sub); err != nil {
		log.Printf("delegation disconnect: %v", err)
		if writeStoreFailure(w, err) {
			return
		}
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "Synaplan no longer works for you, but the sign-in service could not confirm it. Try again.",
			Code:  "revoke_unconfirmed",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": false})
}

// writeStoreFailure answers a change to the permission that the store did
// not take, and reports whether err was one.
func writeStoreFailure(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, delegation.ErrUnavailable):
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "Synaplan could not update this permission. Try again.",
			Code:  "store_unavailable",
		})
	case errors.Is(err, delegation.ErrBusy):
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: "This permission was changing at the same time. Try again.",
			Code:  "permission_busy",
		})
	default:
		return false
	}
	return true
}

// delegatedBearer is the Synaplan token for sub, minted from their offline
// token. It authenticates every call made while the person is away.
func (h *Handler) delegatedBearer(ctx context.Context, sub string) (string, error) {
	if !h.delegation.Configured() {
		return "", errNoDelegation
	}
	return h.delegation.Token(ctx, sub, h.audience)
}
