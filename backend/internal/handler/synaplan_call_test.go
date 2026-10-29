package handler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/synaplanauth"
)

func TestSynaplanDoRefusesABodyOverTheLimit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 1025))
	}))
	defer upstream.Close()
	h := &Handler{synaplanURL: upstream.URL, httpClient: upstream.Client()}

	_, _, err := h.synaplanDo(context.Background(), http.MethodGet, "/file", nil, callOptions{limit: 1024})
	if !errors.Is(err, errTooLarge) {
		t.Fatalf("err = %v, want errTooLarge", err)
	}

	status, body, err := h.synaplanDo(context.Background(), http.MethodGet, "/file", nil, callOptions{limit: 1025})
	if err != nil || status != http.StatusOK || len(body) != 1025 {
		t.Fatalf("exact limit: status %d len %d err %v", status, len(body), err)
	}
}

func TestSynaplanDoSendsTheInstanceCredentialWithoutAUserToken(t *testing.T) {
	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	editorCalls := 0
	h := &Handler{
		synaplanURL: upstream.URL,
		httpClient:  upstream.Client(),
		editor: func(_ context.Context, req *http.Request) error {
			editorCalls++
			req.Header.Set("Authorization", "Bearer user")
			return nil
		},
	}
	h.setCredential("pi_1", "s3cret")
	_, _, err := h.synaplanDo(context.Background(), http.MethodGet, "/x", nil, callOptions{skipUser: true, extra: h.instanceHeaders()})
	if err != nil {
		t.Fatal(err)
	}
	if editorCalls != 0 || got.Get("Authorization") != "" {
		t.Fatal("the user token was sent on an instance call")
	}
	if got.Get("X-Instance-Id") != "pi_1" || got.Get("X-Instance-Secret") != "s3cret" {
		t.Fatalf("instance headers = %v", got)
	}
}

// The token OpenCloud's proxy hands this service was issued for OpenCloud.
// Whatever an editor does, it must never reach Synaplan unchanged.
func TestSynaplanDoRefusesToForwardTheOpenCloudToken(t *testing.T) {
	reached := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	const openCloudToken = "eyJ.opencloud.token"
	h := &Handler{
		synaplanURL: upstream.URL,
		httpClient:  upstream.Client(),
		// A broken editor that forwards instead of exchanging.
		editor: func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+openCloudToken)
			return nil
		},
	}
	ctx := synaplanauth.ContextWithOIDCToken(context.Background(), openCloudToken)
	_, _, err := h.synaplanDo(ctx, http.MethodGet, "/x", nil, callOptions{})
	if !errors.Is(err, errOpenCloudTokenForwarded) {
		t.Fatalf("err = %v, want errOpenCloudTokenForwarded", err)
	}
	if reached {
		t.Fatal("the request left for Synaplan with the OpenCloud token")
	}
}

// An exchanged token is a different string, and passes.
func TestSynaplanDoSendsTheExchangedToken(t *testing.T) {
	var got string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	h := &Handler{
		synaplanURL: upstream.URL,
		httpClient:  upstream.Client(),
		editor: func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer exchanged-for-synaplan")
			return nil
		},
	}
	ctx := synaplanauth.ContextWithOIDCToken(context.Background(), "eyJ.opencloud.token")
	if _, _, err := h.synaplanDo(ctx, http.MethodGet, "/x", nil, callOptions{}); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer exchanged-for-synaplan" {
		t.Fatalf("Authorization = %q", got)
	}
}
