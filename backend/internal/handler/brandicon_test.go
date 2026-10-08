package handler

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeSynaplan struct {
	light, dark string
	images      map[string]func(w http.ResponseWriter)
}

func (f *fakeSynaplan) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/config/runtime" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"branding":{"iconUrl":"","resolvedIconUrl":%q,"resolvedIconDarkUrl":%q}}`, f.light, f.dark)
		return
	}
	if image, ok := f.images[r.URL.Path]; ok {
		image(w)
		return
	}
	http.NotFound(w, r)
}

func image(contentType, body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, body)
	}
}

func newBrandIconTest(t *testing.T, fake *fakeSynaplan) *Handler {
	t.Helper()
	upstream := httptest.NewServer(fake)
	t.Cleanup(upstream.Close)
	return &Handler{synaplanURL: upstream.URL + "/", httpClient: upstream.Client()}
}

func getBrandIcon(h *Handler, query string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.BrandIcon(rr, httptest.NewRequest(http.MethodGet, "/api/synaplan/assets/brand-icon"+query, nil))
	return rr
}

func TestBrandIcon_ReplaysTheIconSynaplanResolved(t *testing.T) {
	h := newBrandIconTest(t, &fakeSynaplan{
		light: "/single_bird-dark.svg",
		dark:  "/brand/dark.png",
		images: map[string]func(w http.ResponseWriter){
			"/single_bird-dark.svg": image("image/svg+xml", "<svg>bird</svg>"),
			"/brand/dark.png":       image("image/png", "DARK"),
		},
	})

	cases := []struct{ query, wantType, wantBody string }{
		{"", "image/svg+xml", "<svg>bird</svg>"},
		{"?theme=light", "image/svg+xml", "<svg>bird</svg>"},
		{"?theme=dark", "image/png", "DARK"},
		{"?theme=sepia", "image/svg+xml", "<svg>bird</svg>"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			rr := getBrandIcon(h, tc.query)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rr.Code)
			}
			if got := rr.Header().Get("Content-Type"); got != tc.wantType {
				t.Errorf("Content-Type = %q, want %q", got, tc.wantType)
			}
			if got := rr.Body.String(); got != tc.wantBody {
				t.Errorf("body = %q, want %q", got, tc.wantBody)
			}
			if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if got := rr.Header().Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
				t.Errorf("Content-Security-Policy = %q, want a sandbox policy", got)
			}
			if got := rr.Header().Get("Cache-Control"); got != "public, max-age=300" {
				t.Errorf("Cache-Control = %q, want public, max-age=300", got)
			}
		})
	}
}

func TestBrandIcon_AbsoluteIconURL(t *testing.T) {
	fake := &fakeSynaplan{images: map[string]func(w http.ResponseWriter){
		"/cdn/icon.png": image("image/png", "CDN"),
	}}
	h := newBrandIconTest(t, fake)
	fake.light = h.synaplanURL + "cdn/icon.png"

	rr := getBrandIcon(h, "")

	if rr.Code != http.StatusOK || rr.Body.String() != "CDN" {
		t.Errorf("got %d %q, want 200 CDN", rr.Code, rr.Body.String())
	}
}

func TestBrandIcon_SubpathDeployment(t *testing.T) {
	fake := &fakeSynaplan{light: "/single_bird-dark.svg", images: map[string]func(w http.ResponseWriter){
		"/single_bird-dark.svg": image("image/svg+xml", "<svg>bird</svg>"),
	}}
	upstream := httptest.NewServer(http.StripPrefix("/synaplan", fake))
	defer upstream.Close()
	h := &Handler{synaplanURL: upstream.URL + "/synaplan", httpClient: upstream.Client()}

	rr := getBrandIcon(h, "")

	if rr.Code != http.StatusOK || rr.Body.String() != "<svg>bird</svg>" {
		t.Errorf("got %d %q, want the bird", rr.Code, rr.Body.String())
	}
}

func TestBrandIcon_FailsWithoutFallback(t *testing.T) {
	images := map[string]func(w http.ResponseWriter){
		"/page.html":            image("text/html", "<script>alert(1)</script>"),
		"/huge.png":             image("image/png", strings.Repeat("x", brandIconMaxBytes+1)),
		"/single_bird-dark.svg": image("image/svg+xml", "<svg>bird</svg>"),
	}
	cases := map[string]string{
		"icon missing":               "/gone.png",
		"icon is not an image":       "/page.html",
		"icon too large":             "/huge.png",
		"unsupported scheme":         "file:///etc/passwd",
		"Synaplan without the field": "",
	}
	for name, light := range cases {
		t.Run(name, func(t *testing.T) {
			h := newBrandIconTest(t, &fakeSynaplan{light: light, dark: "/single_bird-dark.svg", images: images})

			rr := getBrandIcon(h, "?theme=light")

			if rr.Code != http.StatusBadGateway {
				t.Errorf("status = %d, want 502", rr.Code)
			}
			if strings.Contains(rr.Body.String(), "bird") {
				t.Errorf("served the bird instead of failing: %q", rr.Body.String())
			}
		})
	}
}

func TestBrandIcon_SynaplanUnreachable(t *testing.T) {
	h := &Handler{synaplanURL: "http://127.0.0.1:1", httpClient: &http.Client{}}

	rr := getBrandIcon(h, "")

	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rr.Code)
	}
}
