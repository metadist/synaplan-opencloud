// Package handler hosts the HTTP handlers for the synaplan-opencloud
// backend. Each exposed endpoint lives in its own file (me.go,
// translate.go, ...) and shares the Handler struct defined here.
package handler

import (
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
	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

// Handler holds the state shared by all endpoints:
//
//   - synaplanAPI is the generated Synaplan client with an outbound
//     auth editor registered (either OIDC token exchange or a shared
//     API key, picked at startup in pkg/command/server.go), so every
//     call is authenticated without per-handler boilerplate.
//   - cs3 reads file bytes from OpenCloud storage via the reva
//     gateway for file-based operations like /translate.
//   - assetProxy streams whitelisted brand assets from the paired
//     Synaplan install for the public /api/synaplan/assets endpoint.
//
// The Synaplan API client has no global HTTP timeout — per-call
// timeouts are set on the request context instead. /me uses the
// incoming request's context (cancelled when the client disconnects);
// /translate sets a generous context.WithTimeout because LLM calls
// can take minutes.
type Handler struct {
	synaplanURL string
	synaplanAPI *synaplanapi.ClientWithResponses
	cs3         *cs3reader.Reader
	assetProxy  *httputil.ReverseProxy
}

// New creates a new Handler. The editor is the synaplanapi
// RequestEditorFn that authenticates every outbound call to Synaplan
// — the caller picks between per-user OIDC token exchange and a
// shared API key (see pkg/command/server.go).
func New(editor synaplanapi.RequestEditorFn, synaplanURL string, cs3 *cs3reader.Reader) (*Handler, error) {
	apiClient, err := synaplanapi.NewClientWithResponses(
		synaplanURL,
		synaplanapi.WithHTTPClient(&http.Client{}),
		synaplanapi.WithRequestEditorFn(editor),
	)
	if err != nil {
		return nil, fmt.Errorf("creating synaplan API client: %w", err)
	}

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

	return &Handler{
		synaplanURL: synaplanURL,
		synaplanAPI: apiClient,
		cs3:         cs3,
		assetProxy:  assetProxy,
	}, nil
}

// writeJSON serialises v as JSON to w with the given HTTP status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("failed to encode response: %v", err)
	}
}
