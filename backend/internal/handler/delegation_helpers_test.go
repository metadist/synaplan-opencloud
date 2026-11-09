package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/delegation"
)

func tokenFor(sub string) string {
	payload, _ := json.Marshal(map[string]string{"sub": sub})
	return "h." + base64.RawURLEncoding.EncodeToString(payload) + ".s"
}

type identityProvider struct {
	*httptest.Server
	mu      sync.Mutex
	revoked []string
}

func (p *identityProvider) revokedTokens() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.revoked...)
}

// fakeIdentityProvider issues an offline token for sub on any code, and
// exchanges any token for "for-<audience>".
func fakeIdentityProvider(t *testing.T, sub string) *identityProvider {
	t.Helper()
	p := &identityProvider{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path == "/revoke" {
			p.mu.Lock()
			p.revoked = append(p.revoked, r.Form.Get("token"))
			p.mu.Unlock()
			return
		}
		answer := map[string]any{"expires_in": 300, "access_token": tokenFor(sub), "refresh_token": "offline-" + sub}
		if r.Form.Get("grant_type") == "urn:ietf:params:oauth:grant-type:token-exchange" {
			answer = map[string]any{"expires_in": 300, "access_token": "for-" + r.Form.Get("audience")}
		}
		_ = json.NewEncoder(w).Encode(answer)
	}))
	return p
}

func newDelegation(idpURL string) *delegation.Delegation {
	return delegation.New(delegation.Config{
		AuthorizeEndpoint:  "https://idp/auth",
		TokenEndpoint:      idpURL,
		RevocationEndpoint: idpURL + "/revoke",
		ClientID:           "synaplan-opencloud",
		ClientSecret:       "secret",
	}, delegation.NewMemoryStore())
}

// connectedDelegation is a Delegation in which sub has allowed work while
// they are away.
func connectedDelegation(t *testing.T, idpURL, sub string) *delegation.Delegation {
	t.Helper()
	d := newDelegation(idpURL)
	if err := d.Connect(context.Background(), "code", "verifier", "https://cloud/callback", sub); err != nil {
		t.Fatal(err)
	}
	return d
}
