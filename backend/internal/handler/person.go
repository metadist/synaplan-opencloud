package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/davfiles"
	"github.com/metadist/synaplan-opencloud/internal/delegation"
	"github.com/metadist/synaplan-opencloud/internal/synaplanauth"
)

// personFiles is what work done for a person needs from their files. The
// browser request uses cs3reader with the person's own session; work done
// while the person is away uses davfiles with a token issued for them.
type personFiles interface {
	StatAny(ctx context.Context, id string) (cs3reader.Meta, error)
	List(ctx context.Context, id string) ([]cs3reader.Child, error)
	Open(ctx context.Context, id string) (*cs3reader.File, error)
	SaveChild(ctx context.Context, folderID, filename string, content []byte) (cs3reader.Written, error)
	SaveToFolder(ctx context.Context, folder, filename string, content []byte) (cs3reader.Written, error)
	Save(ctx context.Context, filename string, content []byte) (cs3reader.Written, error)
}

// currentUserID is the signed-in person as Synaplan knows them: the subject
// of the identity provider's token. It is the external_id of their identity
// row and the key of everything this service keeps per person.
func currentUserID(ctx context.Context) string {
	return delegation.Subject(synaplanauth.OIDCTokenFromContext(ctx))
}

// awayFiles reaches the person's files while they are away, as them.
func (h *Handler) awayFiles(sub string) *davfiles.Client {
	return davfiles.New(h.settings.OpenCloudURL, h.openCloudClient(), func(ctx context.Context) (string, error) {
		return h.delegation.Token(ctx, sub, h.settings.OpenCloudAudience)
	})
}

// canActAway reports whether work for sub can run without them: the
// person connected and this service runs with organisation login.
func (h *Handler) canActAway(sub string) bool {
	return h.delegation.Configured() && h.delegation.Connected(sub)
}

// DelegationStatus tells the browser whether background work is possible
// at all and whether the signed-in person has connected.
func (h *Handler) DelegationStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"available":         h.delegation.Configured(),
		"connected":         h.delegation.Connected(currentUserID(r.Context())),
		"clientId":          h.settings.OIDCClientID,
		"authorizeEndpoint": h.settings.OIDCAuthorizeEndpoint,
	})
}

// DelegationConnect redeems the authorization code the browser obtained
// for this service's client. The resulting offline token must belong to the
// signed-in person; it is then bound to their Synaplan login.
func (h *Handler) DelegationConnect(w http.ResponseWriter, r *http.Request) {
	if !h.delegation.Configured() {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "Background work needs organisation login.", Code: "not_available"})
		return
	}
	var req struct {
		Code        string `json:"code"`
		Verifier    string `json:"verifier"`
		RedirectURI string `json:"redirectUri"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil || req.Code == "" || req.Verifier == "" || req.RedirectURI == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "The sign-in answer is incomplete.", Code: "invalid_request"})
		return
	}
	sub := currentUserID(r.Context())
	if err := h.delegation.Connect(r.Context(), req.Code, req.Verifier, req.RedirectURI, sub); err != nil {
		log.Printf("delegation connect: %v", err)
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Synaplan could not be allowed to work for you. Try again.", Code: "connect_failed"})
		return
	}
	if h.hasInstance() {
		if _, _, err := h.bindLogin(r.Context(), sub); err != nil {
			log.Printf("sso bind after connect: %v", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true})
}

// DelegationDisconnect forgets the signed-in person's offline token.
func (h *Handler) DelegationDisconnect(w http.ResponseWriter, r *http.Request) {
	h.delegation.Disconnect(currentUserID(r.Context()))
	writeJSON(w, http.StatusOK, map[string]any{"connected": false})
}

// personBearer is the Synaplan token for sub, minted from their offline
// token. It is how every background call to Synaplan is authenticated.
func (h *Handler) personBearer(ctx context.Context, sub string) (string, error) {
	if !h.delegation.Configured() {
		return "", errors.New("background work needs organisation login")
	}
	return h.delegation.Token(ctx, sub, h.settings.SynaplanAudience)
}
