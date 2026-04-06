package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// allowedAssets pins the filenames proxyable through /api/synaplan/assets.
// Explicit allowlist → no open-relay SSRF via a free-form path param.
var allowedAssets = map[string]struct{}{
	"single_bird.svg":       {},
	"single_bird-light.svg": {},
	"single_bird-dark.svg":  {},
}

// Asset proxies a whitelisted brand asset from the paired Synaplan
// install. Unauthenticated — <img> can't send a Bearer token.
func (h *Handler) Asset(w http.ResponseWriter, r *http.Request) {
	if _, ok := allowedAssets[chi.URLParam(r, "name")]; !ok {
		http.NotFound(w, r)
		return
	}
	h.assetProxy.ServeHTTP(w, r)
}
