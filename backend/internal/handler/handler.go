// Package handler hosts the HTTP handlers for the synaplan-opencloud
// backend. Each exposed endpoint lives in its own file (me.go,
// translate.go, ...) and shares the Handler struct defined here.
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/delegation"
	"github.com/metadist/synaplan-opencloud/internal/notify"
	"github.com/metadist/synaplan-opencloud/internal/preferences"
	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
	"github.com/metadist/synaplan-opencloud/internal/synaplanauth"
)

// Delegated is what acting as a person needs: their offline tokens, the
// exchange that proves who the signed-in person is, the Synaplan client
// their tokens are exchanged for, and their own settings. It is nil with
// the shared API key, which knows no person.
type Delegated struct {
	Tokens      *delegation.Delegation
	Exchanger   synaplanauth.TokenExchanger
	Audience    string
	Preferences preferences.Store
}

// folderPaths finds where a folder lives, as the signed-in person sees it.
type folderPaths interface {
	FolderPath(ctx context.Context, resourceID string) (string, error)
}

// Handler holds the state shared by all endpoints:
//
//   - synaplanAPI is the generated Synaplan client, the one way to
//     Synaplan's API. Its editors pick the credential (see
//     authenticate) and refuse the incoming OpenCloud token, so every
//     call is authenticated without per-handler boilerplate.
//   - cs3 reads file bytes from OpenCloud storage via the reva
//     gateway for file-based operations like /translate.
//   - assetProxy streams whitelisted brand assets from the paired
//     Synaplan install for the public /api/synaplan/assets endpoint.
//   - delegation holds the offline tokens of people who allowed this
//     service to act for them while they are away.
//   - preferences keeps each person's own settings; folders resolves
//     the folders they name to the paths OpenCloud knows.
//   - notifications verifies what Synaplan sends to /inbound.
//
// The Synaplan API client has no global HTTP timeout — per-call
// timeouts are set on the request context instead. /me uses the
// incoming request's context (cancelled when the client disconnects);
// /translate sets a generous context.WithTimeout because LLM calls
// can take minutes.
type Handler struct {
	synaplanURL string
	synaplanAPI *synaplanapi.ClientWithResponses
	editor      synaplanapi.RequestEditorFn
	httpClient  *http.Client
	cs3         *cs3reader.Reader
	assetProxy  *httputil.ReverseProxy
	delegation  *delegation.Delegation
	exchanger   synaplanauth.TokenExchanger
	audience    string
	preferences preferences.Store
	folders     folderPaths

	notifications notifications

	// maxCopyBytes is SYNAPLAN_MAX_COPY_BYTES. Zero means the default.
	maxCopyBytes int64
	// sharedKey is set when every Synaplan call uses one API key.
	// Listing files then lists the key owner's files, for every
	// signed-in OpenCloud user, so From Synaplan stays closed.
	sharedKey bool
}

// UseSharedKey records that outbound Synaplan calls use one API key.
func (h *Handler) UseSharedKey() {
	h.sharedKey = true
}

// New creates a new Handler. The editor is the synaplanapi
// RequestEditorFn that authenticates every outbound call to Synaplan
// — the caller picks between per-user OIDC token exchange and a
// shared API key (see pkg/command/server.go).
func New(editor synaplanapi.RequestEditorFn, synaplanURL string, cs3 *cs3reader.Reader, delegated *Delegated) (*Handler, error) {
	target, err := url.Parse(synaplanURL)
	if err != nil {
		return nil, fmt.Errorf("parsing synaplan url: %w", err)
	}
	assetProxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// Preserve any path prefix in synaplanURL (subpath deployments)
			// instead of clobbering it with just /name.
			pr.Out.URL.Path = "/" + strings.TrimLeft(path.Join(target.Path, chi.URLParam(pr.In, "name")), "/")
			pr.Out.URL.RawPath = ""
			pr.Out.Host = target.Host
		},
	}

	h := &Handler{
		synaplanURL: synaplanURL,
		editor:      editor,
		httpClient:  &http.Client{},
		cs3:         cs3,
		assetProxy:  assetProxy,
	}
	h.notifications.seen = notify.NewSeen()
	if cs3 != nil {
		h.folders = cs3
	}
	if delegated != nil {
		h.preferences = delegated.Preferences
		h.delegation = delegated.Tokens
		h.exchanger = delegated.Exchanger
		h.audience = delegated.Audience
	}
	h.synaplanAPI, err = synaplanapi.NewClientWithResponses(
		synaplanURL,
		synaplanapi.WithHTTPClient(&http.Client{}),
		synaplanapi.WithRequestEditorFn(h.authenticate),
		synaplanapi.WithRequestEditorFn(refuseOpenCloudToken),
	)
	if err != nil {
		return nil, fmt.Errorf("creating synaplan API client: %w", err)
	}
	return h, nil
}

// writeJSON serialises v as JSON to w with the given HTTP status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("failed to encode response: %v", err)
	}
}
