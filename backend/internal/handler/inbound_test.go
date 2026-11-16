package handler

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const synaplanIssuer = "https://synaplan.example"

// signedSynaplan serves Synaplan's open configuration and key set, and
// signs notifications with that key.
type signedSynaplan struct {
	*httptest.Server
	key ed25519.PrivateKey
}

func newSignedSynaplan(t *testing.T) *signedSynaplan {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s := &signedSynaplan{key: key}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/synaplan-configuration":
			_, _ = fmt.Fprintf(w, `{"issuer":%q}`, synaplanIssuer)
		case "/.well-known/jwks.json":
			_, _ = fmt.Fprintf(w, `{"keys":[{"kty":"OKP","crv":"Ed25519","alg":"EdDSA","kid":"k1","x":%q}]}`, base64.RawURLEncoding.EncodeToString(pub))
		default:
			http.NotFound(w, r)
		}
	}))
	return s
}

func (s *signedSynaplan) notification(t *testing.T, claims map[string]any) string {
	t.Helper()
	now := time.Now().Unix()
	full := jwt.MapClaims{"iss": synaplanIssuer, "aud": notificationAudience, "iat": now, "exp": now + 300}
	for k, v := range claims {
		full[k] = v
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, full)
	token.Header["typ"] = "synaplan-notification+jwt"
	token.Header["kid"] = "k1"
	signed, err := token.SignedString(s.key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func inbound(h *Handler, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.Inbound(rec, httptest.NewRequest(http.MethodPost, "/api/synaplan/inbound", strings.NewReader(token)))
	return rec
}

func TestARevokedIdentityEndsWorkWhileAway(t *testing.T) {
	synaplan := newSignedSynaplan(t)
	defer synaplan.Close()
	idp := fakeIdentityProvider(t, "ada")
	h, err := New(nil, synaplan.URL, nil, delegated(t, idp, "ada"), Admin{})
	if err != nil {
		t.Fatal(err)
	}
	token := synaplan.notification(t, map[string]any{"type": "identity_revoked", "id": "7", "sub": "ada"})

	if rec := inbound(h, token); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d %s, want 204", rec.Code, rec.Body)
	}
	if connectedAda(t, h) {
		t.Fatal("still acting for a person Synaplan revoked")
	}
	if got := idp.revokedTokens(); len(got) != 1 || got[0] != "offline-ada" {
		t.Fatalf("revoked %v, want the offline token", got)
	}
	if rec := inbound(h, token); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "replay") {
		t.Fatalf("replay: status %d %s, want 401 replay", rec.Code, rec.Body)
	}
}

func TestInboundRefusesWhatDoesNotVerify(t *testing.T) {
	synaplan := newSignedSynaplan(t)
	defer synaplan.Close()
	other := newSignedSynaplan(t)
	defer other.Close()
	idp := fakeIdentityProvider(t, "ada")
	cases := map[string]struct {
		token string
		want  string
	}{
		"not a token":      {"garbage", "bad_signature"},
		"another key":      {other.notification(t, map[string]any{"type": "identity_revoked", "id": "1", "sub": "ada"}), "bad_signature"},
		"another issuer":   {synaplan.notification(t, map[string]any{"iss": "https://elsewhere.example", "type": "identity_revoked", "id": "2", "sub": "ada"}), "wrong_issuer"},
		"another instance": {synaplan.notification(t, map[string]any{"aud": "pi_other", "type": "identity_revoked", "id": "3", "sub": "ada"}), "wrong_instance"},
		"expired":          {synaplan.notification(t, map[string]any{"iat": time.Now().Unix() - 600, "exp": time.Now().Unix() - 300, "type": "identity_revoked", "id": "4", "sub": "ada"}), "stale"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h, err := New(nil, synaplan.URL, nil, delegated(t, idp, "ada"), Admin{})
			if err != nil {
				t.Fatal(err)
			}
			rec := inbound(h, tc.token)
			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("status %d %s, want 401 %s", rec.Code, rec.Body, tc.want)
			}
			if !connectedAda(t, h) {
				t.Fatal("an unverified notification ended work while away")
			}
		})
	}
}

func TestInboundIsOffWithoutOrganisationLogin(t *testing.T) {
	h, err := New(nil, "http://synaplan.invalid", nil, nil, Admin{})
	if err != nil {
		t.Fatal(err)
	}
	if rec := inbound(h, "anything"); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

// Inbound is open to anyone. Requests that pass the checks needing no key
// must not each turn into a request to Synaplan while it is failing.
func TestAFailingKeyFetchIsNotRepeatedForEveryRequest(t *testing.T) {
	var fetches atomic.Int32
	synaplan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer synaplan.Close()
	signer := newSignedSynaplan(t)
	defer signer.Close()
	h, err := New(nil, synaplan.URL, nil, delegated(t, fakeIdentityProvider(t, "ada"), "ada"), Admin{})
	if err != nil {
		t.Fatal(err)
	}

	for i := range 5 {
		token := signer.notification(t, map[string]any{"type": "identity_revoked", "id": fmt.Sprint(i), "sub": "ada"})
		if rec := inbound(h, token); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401 while keys are unknown", rec.Code)
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("Synaplan was asked %d times, want once", got)
	}
}

// When the token store cannot remove a revoked person's offline token,
// Inbound must fail (not 204) and leave the notification id unrecorded so
// Synaplan's retry succeeds once the store is reachable again.
func TestInboundReportsStoreFailureAndAllowsRetry(t *testing.T) {
	synaplan := newSignedSynaplan(t)
	defer synaplan.Close()
	idp := fakeIdentityProvider(t, "ada")
	store := &switchableStore{}
	del := &Delegated{
		Tokens:    newDelegationWithStore(idp, store),
		Exchanger: exchanger{"u-ada": "ada"},
		Audience:  "synaplan-app",
	}
	if err := del.Tokens.Connect(t.Context(), "code", "verifier", "https://cloud/callback", "ada"); err != nil {
		t.Fatal(err)
	}
	h, err := New(nil, synaplan.URL, nil, del, Admin{})
	if err != nil {
		t.Fatal(err)
	}
	token := synaplan.notification(t, map[string]any{"type": "identity_revoked", "id": "42", "sub": "ada"})

	store.down.Store(true)
	if rec := inbound(h, token); rec.Code != http.StatusBadGateway {
		t.Fatalf("status while store is down = %d %s, want 502", rec.Code, rec.Body)
	}

	store.down.Store(false)
	if !connectedAda(t, h) {
		t.Fatal("expected token to still be stored before retry")
	}
	if rec := inbound(h, token); rec.Code != http.StatusNoContent {
		t.Fatalf("retry status = %d %s, want 204", rec.Code, rec.Body)
	}
	if connectedAda(t, h) {
		t.Fatal("still connected after successful retry")
	}
}
