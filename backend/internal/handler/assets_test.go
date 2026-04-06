package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// newAssetTestHandler spins up a Handler with just enough state for
// the asset proxy tests: a reverse-proxy pointed at `target`, no
// Synaplan API client, no CS3 reader. newHandler can't be used
// because it tries to contact OIDC for the real request editor.
func newAssetTestHandler(target *url.URL) *Handler {
	return &Handler{
		assetProxy: &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				pr.Out.URL.Path = "/" + strings.TrimLeft(
					path.Join(target.Path, chi.URLParam(pr.In, "name")),
					"/",
				)
				pr.Out.URL.RawPath = ""
				pr.Out.Host = target.Host
			},
		},
	}
}

// routeAsset mirrors the real route registration in pkg/command/server.go
// so chi.URLParam("name") is populated inside the handler.
func routeAsset(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Get("/api/synaplan/assets/{name}", h.Asset)
	return r
}

func TestAsset_ProxiesAllowlistedFile(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = io.WriteString(w, "<svg/>")
	}))
	defer upstream.Close()

	target, _ := url.Parse(upstream.URL)
	h := newAssetTestHandler(target)

	req := httptest.NewRequest(http.MethodGet, "/api/synaplan/assets/single_bird-dark.svg", nil)
	rr := httptest.NewRecorder()
	routeAsset(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Errorf("Content-Type = %q, want image/svg+xml", got)
	}
	if body := rr.Body.String(); body != "<svg/>" {
		t.Errorf("body = %q, want <svg/>", body)
	}
	if gotPath != "/single_bird-dark.svg" {
		t.Errorf("upstream received path = %q, want /single_bird-dark.svg", gotPath)
	}
}

func TestAsset_RejectsNonAllowlistedNames(t *testing.T) {
	var upstreamHit bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
	}))
	defer upstream.Close()

	target, _ := url.Parse(upstream.URL)
	h := newAssetTestHandler(target)

	cases := []string{
		"evil.svg",
		"single_bird.svg.bak",
		"../../../etc/passwd",
		"single_bird",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			upstreamHit = false
			req := httptest.NewRequest(
				http.MethodGet,
				"/api/synaplan/assets/"+url.PathEscape(name),
				nil,
			)
			rr := httptest.NewRecorder()
			routeAsset(h).ServeHTTP(rr, req)

			if rr.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rr.Code)
			}
			if upstreamHit {
				t.Errorf("upstream was hit for rejected name %q", name)
			}
		})
	}
}

func TestAsset_UpstreamErrorBecomesBadGateway(t *testing.T) {
	// Point at a non-routable address so the proxy's dial fails fast.
	target, _ := url.Parse("http://127.0.0.1:1")
	h := newAssetTestHandler(target)

	req := httptest.NewRequest(http.MethodGet, "/api/synaplan/assets/single_bird-dark.svg", nil)
	rr := httptest.NewRecorder()
	routeAsset(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rr.Code)
	}
}

func TestAsset_TrimsDoubleSlashesFromSynaplanURL(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
	}))
	defer upstream.Close()

	target, _ := url.Parse(strings.TrimRight(upstream.URL, "/") + "/")
	h := newAssetTestHandler(target)

	req := httptest.NewRequest(http.MethodGet, "/api/synaplan/assets/single_bird-light.svg", nil)
	rr := httptest.NewRecorder()
	routeAsset(h).ServeHTTP(rr, req)

	if gotPath != "/single_bird-light.svg" {
		t.Errorf("upstream path = %q, want /single_bird-light.svg", gotPath)
	}
}

func TestAsset_PreservesSubpathInSynaplanURL(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
	}))
	defer upstream.Close()

	for _, suffix := range []string{"/synaplan", "/synaplan/", "/deep/sub"} {
		t.Run(suffix, func(t *testing.T) {
			gotPath = ""
			target, _ := url.Parse(upstream.URL + suffix)
			h := newAssetTestHandler(target)

			req := httptest.NewRequest(http.MethodGet, "/api/synaplan/assets/single_bird-dark.svg", nil)
			rr := httptest.NewRecorder()
			routeAsset(h).ServeHTTP(rr, req)

			want := strings.TrimRight(suffix, "/") + "/single_bird-dark.svg"
			if gotPath != want {
				t.Errorf("upstream path = %q, want %q", gotPath, want)
			}
		})
	}
}
