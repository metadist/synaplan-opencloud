package idp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func issuer(t *testing.T, publish map[string]string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/.well-known/openid-configuration" || publish == nil {
			http.NotFound(w, r)
			return
		}
		doc := map[string]string{"issuer": srv.URL, "jwks_uri": srv.URL + "/certs"}
		for k, v := range publish {
			doc[k] = srv.URL + v
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

var complete = map[string]string{
	"authorization_endpoint": "/auth",
	"token_endpoint":         "/token",
	"revocation_endpoint":    "/revoke",
}

func TestEndpoints(t *testing.T) {
	cases := map[string]struct {
		publish   map[string]string
		override  string
		wantToken string
		wantErr   bool
	}{
		"published":           {complete, "", "/token", false},
		"token overridden":    {complete, "http://internal/token", "http://internal/token", false},
		"no revocation":       {map[string]string{"authorization_endpoint": "/auth", "token_endpoint": "/token"}, "", "", true},
		"no discovery at all": {nil, "", "", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _ := issuer(t, tc.publish)
			got, err := New(srv.URL, tc.override, srv.Client()).Endpoints(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v", err)
			}
			if tc.wantErr {
				return
			}
			want := tc.wantToken
			if tc.override == "" {
				want = srv.URL + tc.wantToken
			}
			if got.Token != want || got.Authorization != srv.URL+"/auth" || got.Revocation != srv.URL+"/revoke" {
				t.Fatalf("endpoints = %+v", got)
			}
		})
	}
}

func TestDiscoveryRunsOnceAndBacksOffAfterAFailure(t *testing.T) {
	srv, calls := issuer(t, complete)
	p := New(srv.URL, "", srv.Client())
	for range 3 {
		if _, err := p.Endpoints(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("asked %d times, want once", calls.Load())
	}

	broken, brokenCalls := issuer(t, nil)
	q := New(broken.URL, "", broken.Client())
	for range 3 {
		if _, err := q.Endpoints(context.Background()); err == nil {
			t.Fatal("a missing discovery document was accepted")
		}
	}
	if brokenCalls.Load() != 1 {
		t.Fatalf("a failing issuer was asked %d times, want once", brokenCalls.Load())
	}
}

func TestAnOverriddenTokenEndpointNeedsNoDiscovery(t *testing.T) {
	got, err := New("", "http://internal/token", nil).TokenEndpoint(context.Background())
	if err != nil || got != "http://internal/token" {
		t.Fatalf("TokenEndpoint() = %q, %v", got, err)
	}
}
