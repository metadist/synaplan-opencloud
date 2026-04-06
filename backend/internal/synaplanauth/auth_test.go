package synaplanauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewAPIKeyRequestEditor_SetsHeader(t *testing.T) {
	const key = "sk_test1234567890abcdef"
	editor := NewAPIKeyRequestEditor(key)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := editor(context.Background(), req); err != nil {
		t.Fatalf("editor returned error: %v", err)
	}

	if got := req.Header.Get("X-API-Key"); got != key {
		t.Errorf("X-API-Key = %q, want %q", got, key)
	}
}

func TestNewAPIKeyRequestEditor_DoesNotTouchAuthorization(t *testing.T) {
	// The OIDC editor sets the Authorization header. The API-key
	// editor must NOT — Synaplan's ApiKeyAuthenticator routes
	// requests by header presence, and an unrelated stale Bearer
	// token would cause it to defer to the wrong authenticator.
	editor := NewAPIKeyRequestEditor("sk_abc")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer should-stay-untouched")

	if err := editor(context.Background(), req); err != nil {
		t.Fatalf("editor returned error: %v", err)
	}

	if got := req.Header.Get("Authorization"); got != "Bearer should-stay-untouched" {
		t.Errorf("Authorization header was modified: got %q", got)
	}
}

func TestNewAPIKeyRequestEditor_DoesNotRequireContextValues(t *testing.T) {
	// The whole point of API-key mode is independence from the
	// per-user OIDC context. Calling with a bare context.Background()
	// (no OIDC token, no reva user) must succeed.
	editor := NewAPIKeyRequestEditor("sk_xyz")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := editor(context.Background(), req); err != nil {
		t.Errorf("editor returned error on bare context: %v", err)
	}
}
