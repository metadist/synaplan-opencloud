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

	"github.com/go-chi/chi/v5"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
	"github.com/metadist/synaplan-opencloud/internal/synaplanauth"
)

// Handler holds the state shared by all endpoints:
//
//   - synaplanAPI is the generated Synaplan client with a
//     synaplanauth request editor registered, so every outgoing call
//     is authenticated on behalf of the end-user whose request
//     triggered it via OIDC token exchange.
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

// New creates a new Handler.
func New(exchanger synaplanauth.TokenExchanger, synaplanURL string, cs3 *cs3reader.Reader) (*Handler, error) {
	apiClient, err := synaplanapi.NewClientWithResponses(
		synaplanURL,
		synaplanapi.WithHTTPClient(&http.Client{}),
		synaplanapi.WithRequestEditorFn(synaplanauth.NewRequestEditor(exchanger)),
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
			pr.Out.URL.Path = "/" + chi.URLParam(pr.In, "name")
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
