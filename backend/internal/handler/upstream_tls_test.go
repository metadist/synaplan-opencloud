package handler

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpstreamClientReachesSynaplanWhenVerificationIsSkipped(t *testing.T) {
	var sawAsset, sawIcon bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/single_bird.svg":
			sawAsset = true
			_, _ = io.WriteString(w, "<svg/>")
		case "/icon.svg":
			sawIcon = true
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	h := newTLSHandler(t, srv.URL, skipVerifyClient())

	req := httptest.NewRequest(http.MethodGet, "/api/synaplan/assets/single_bird.svg", nil)
	rr := httptest.NewRecorder()
	routeAsset(h).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != "<svg/>" {
		t.Fatalf("asset status %d body %q", rr.Code, rr.Body.String())
	}
	if !sawAsset {
		t.Fatal("asset proxy did not reach Synaplan")
	}

	resp, err := h.getFromSynaplan(context.Background(), "/icon.svg")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if !sawIcon {
		t.Fatal("key and icon client did not reach Synaplan")
	}

	if _, err := h.synaplanAPI.SynaplanConfigurationWithResponse(context.Background()); err != nil && strings.Contains(err.Error(), "certificate") {
		t.Fatalf("API client rejected the certificate: %v", err)
	}
}

func TestUpstreamClientRejectsUnknownCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := newTLSHandler(t, srv.URL, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/synaplan/assets/single_bird.svg", nil)
	rr := httptest.NewRecorder()
	routeAsset(h).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("asset status %d, want 502", rr.Code)
	}

	if _, err := h.getFromSynaplan(context.Background(), "/icon.svg"); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("icon err %v, want a certificate failure", err)
	}
	if _, err := h.synaplanAPI.SynaplanConfigurationWithResponse(context.Background()); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("API err %v, want a certificate failure", err)
	}
}

func newTLSHandler(t *testing.T, synaplanURL string, upstream *http.Client) *Handler {
	t.Helper()
	h, err := New(func(context.Context, *http.Request) error { return nil }, synaplanURL, nil, nil, Admin{})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.SetUpstream(upstream); err != nil {
		t.Fatal(err)
	}
	return h
}

func skipVerifyClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // the case under test
	return &http.Client{Transport: transport}
}
