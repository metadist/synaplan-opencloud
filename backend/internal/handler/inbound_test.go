package handler

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/metadist/synaplan-opencloud/internal/delegation"
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
			_, _ = fmt.Fprintf(w, `{"keys":[{"kty":"OKP","crv":"Ed25519","x":%q}]}`, base64.RawURLEncoding.EncodeToString(pub))
		default:
			http.NotFound(w, r)
		}
	}))
	return s
}

func (s *signedSynaplan) notification(t *testing.T, claims map[string]any) string {
	t.Helper()
	now := time.Now().Unix()
	full := map[string]any{"iss": synaplanIssuer, "aud": oidcCloudID, "iat": now, "exp": now + 300}
	for k, v := range claims {
		full[k] = v
	}
	header, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "synaplan-notification+jwt"})
	payload, _ := json.Marshal(full)
	signed := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	return signed + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(s.key, []byte(signed)))
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
	defer idp.Close()
	h, err := New(nil, synaplan.URL, nil, connectedDelegation(t, idp.URL, "ada"), "synaplan-app")
	if err != nil {
		t.Fatal(err)
	}
	token := synaplan.notification(t, map[string]any{"type": "identity_revoked", "id": "7", "sub": "ada"})

	if rec := inbound(h, token); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d %s, want 204", rec.Code, rec.Body)
	}
	if h.delegation.Connected("ada") {
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
	defer idp.Close()
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
			h, err := New(nil, synaplan.URL, nil, connectedDelegation(t, idp.URL, "ada"), "synaplan-app")
			if err != nil {
				t.Fatal(err)
			}
			rec := inbound(h, tc.token)
			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("status %d %s, want 401 %s", rec.Code, rec.Body, tc.want)
			}
			if !h.delegation.Connected("ada") {
				t.Fatal("an unverified notification ended work while away")
			}
		})
	}
}

func TestInboundIsOffWithoutOrganisationLogin(t *testing.T) {
	h := &Handler{delegation: delegation.New(delegation.Config{}, delegation.NewMemoryStore())}
	if rec := inbound(h, "anything"); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}
