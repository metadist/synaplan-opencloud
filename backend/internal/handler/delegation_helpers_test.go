package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	userpb "github.com/cs3org/go-cs3apis/cs3/identity/user/v1beta1"
	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"

	"github.com/metadist/synaplan-opencloud/internal/delegation"
	"github.com/metadist/synaplan-opencloud/internal/idp"
	"github.com/metadist/synaplan-opencloud/internal/synaplanauth"
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
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":                 p.URL,
				"authorization_endpoint": p.URL + "/auth",
				"token_endpoint":         p.URL + "/token",
				"revocation_endpoint":    p.URL + "/revoke",
				"jwks_uri":               p.URL + "/certs",
			})
			return
		}
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
	t.Cleanup(p.Close)
	return p
}

// exchanger stands in for the identity provider's token exchange: it knows
// the reva users by id and answers with a token for their subject, and
// refuses any other bearer.
type exchanger map[string]string

func (e exchanger) Exchange(userID, bearer string) (string, error) {
	sub, ok := e[userID]
	if !ok || bearer != tokenFor(sub) {
		return "", errors.New("invalid token")
	}
	return tokenFor(sub), nil
}

func newDelegation(p *identityProvider) *delegation.Delegation {
	return newDelegationWithStore(p, delegation.NewMemoryStore())
}

func newDelegationWithStore(p *identityProvider, store delegation.Store) *delegation.Delegation {
	return delegation.New(delegation.Config{
		Provider:     idp.New(p.URL, idp.Endpoints{}, p.Client()),
		ClientID:     "synaplan-opencloud",
		ClientSecret: "secret",
		HTTPClient:   p.Client(),
		Store:        store,
	})
}

type switchableStore struct {
	mem  *delegation.MemoryStore
	down atomic.Bool
}

func (s *switchableStore) store() *delegation.MemoryStore {
	if s.mem == nil {
		s.mem = delegation.NewMemoryStore()
	}
	return s.mem
}

func (s *switchableStore) Get(ctx context.Context, sub string) (delegation.Entry, bool, error) {
	if s.down.Load() {
		return delegation.Entry{}, false, errors.New("nats down")
	}
	return s.store().Get(ctx, sub)
}

func (s *switchableStore) Write(ctx context.Context, sub, tok string, match uint64) (uint64, bool, error) {
	if s.down.Load() {
		return 0, false, errors.New("nats down")
	}
	return s.store().Write(ctx, sub, tok, match)
}

func (s *switchableStore) Delete(ctx context.Context, sub string, match uint64) (bool, error) {
	if s.down.Load() {
		return false, errors.New("nats down")
	}
	return s.store().Delete(ctx, sub, match)
}

func (s *switchableStore) Lease(ctx context.Context, sub string) (context.Context, func(), error) {
	if s.down.Load() {
		return nil, nil, errors.New("nats down")
	}
	return s.store().Lease(ctx, sub)
}

func (s *switchableStore) Subjects(ctx context.Context) ([]string, error) {
	if s.down.Load() {
		return nil, errors.New("nats down")
	}
	return s.store().Subjects(ctx)
}

func (s *switchableStore) Close() error { return nil }

// delegated is what New needs for people "ada" (reva user u-ada) and "eve"
// (u-eve), with sub connected if it is not empty.
func delegated(t *testing.T, p *identityProvider, sub string) *Delegated {
	t.Helper()
	d := newDelegation(p)
	if sub != "" {
		if err := d.Connect(context.Background(), "code", "verifier", "https://cloud/callback", sub); err != nil {
			t.Fatal(err)
		}
	}
	return &Delegated{Tokens: d, Exchanger: exchanger{"u-ada": "ada", "u-eve": "eve"}, Audience: "synaplan-app"}
}

// signedIn is a request context as OpenCloud's proxy and the middleware
// leave it: the verified reva user and the bearer they sent.
func signedIn(userID, bearer string) context.Context {
	ctx := revactx.ContextSetUser(context.Background(), &userpb.User{Id: &userpb.UserId{OpaqueId: userID}})
	return synaplanauth.ContextWithOIDCToken(ctx, bearer)
}

// connectedAda is whether ada is connected, for a test that expects the
// store to work.
func connectedAda(t *testing.T, h *Handler) bool {
	t.Helper()
	ok, err := h.delegation.Connected(context.Background(), "ada")
	if err != nil {
		t.Fatalf("connected: %v", err)
	}
	return ok
}
