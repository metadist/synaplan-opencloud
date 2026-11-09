package delegation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/idp"
)

func jwt(sub string) string {
	payload, _ := json.Marshal(map[string]string{"sub": sub})
	return "h." + base64.RawURLEncoding.EncodeToString(payload) + ".s"
}

// fakeIdP issues offline-1, offline-2, ... for codes and refreshes, so a
// rotated or replaced token is told apart from the one before.
type fakeIdP struct {
	*httptest.Server
	codeSub   string
	refuse    bool
	revokeURL string
	mu        sync.Mutex
	issued    int
	exchanges int
	refreshed []string
	revoked   []string
}

func newIdP(t *testing.T, codeSub string, refuse bool) *fakeIdP {
	t.Helper()
	f := &fakeIdP{codeSub: codeSub, refuse: refuse}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":                 f.URL,
				"authorization_endpoint": f.URL + "/auth",
				"token_endpoint":         f.URL + "/token",
				"revocation_endpoint":    f.revocation(),
				"jwks_uri":               f.URL + "/certs",
			})
			return
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("client_secret") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path == "/revoke" {
			f.revoked = append(f.revoked, r.Form.Get("token"))
			return
		}
		answer := map[string]any{"expires_in": 300}
		switch r.Form.Get("grant_type") {
		case "refresh_token":
			f.refreshed = append(f.refreshed, r.Form.Get("refresh_token"))
			if f.refuse {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			fallthrough
		case "authorization_code":
			f.issued++
			answer["access_token"] = jwt(f.codeSub)
			answer["refresh_token"] = fmt.Sprintf("offline-%d", f.issued)
		case "urn:ietf:params:oauth:grant-type:token-exchange":
			f.exchanges++
			answer["access_token"] = "for-" + r.Form.Get("audience")
		}
		_ = json.NewEncoder(w).Encode(answer)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeIdP) revocation() string {
	if f.revokeURL != "" {
		return f.revokeURL
	}
	return f.URL + "/revoke"
}

func (f *fakeIdP) state() (exchanges int, refreshed, revoked []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exchanges, append([]string(nil), f.refreshed...), append([]string(nil), f.revoked...)
}

func newDelegation(f *fakeIdP) *Delegation {
	return New(Config{Provider: idp.New(f.URL, idp.Endpoints{}, f.Client()), ClientID: "synaplan-opencloud", ClientSecret: "secret", HTTPClient: f.Client()})
}

func connect(t *testing.T, d *Delegation, sub string) error {
	t.Helper()
	return d.Connect(context.Background(), "code", "verifier", "https://cloud/callback", sub)
}

func TestConnect(t *testing.T) {
	cases := []struct {
		name        string
		codeSub     string
		connections int
		wantErr     bool
		wantRevoked []string
	}{
		{"own code", "ada", 1, false, nil},
		{"someone else's code is revoked, not kept", "bob", 1, true, []string{"offline-1"}},
		{"connecting again revokes the replaced token", "ada", 2, false, []string{"offline-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idp := newIdP(t, tc.codeSub, false)
			d := newDelegation(idp)
			var err error
			for range tc.connections {
				err = connect(t, d, "ada")
			}
			if (err != nil) != tc.wantErr || d.Connected("ada") == tc.wantErr {
				t.Fatalf("err = %v, connected = %v", err, d.Connected("ada"))
			}
			if _, _, revoked := idp.state(); fmt.Sprint(revoked) != fmt.Sprint(tc.wantRevoked) {
				t.Fatalf("revoked %v, want %v", revoked, tc.wantRevoked)
			}
		})
	}
}

func TestTokenRefreshesRotatesExchangesAndCaches(t *testing.T) {
	idp := newIdP(t, "ada", false)
	d := newDelegation(idp)
	if err := connect(t, d, "ada"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		token, err := d.Token(context.Background(), "ada", "synaplan-app")
		if err != nil || token != "for-synaplan-app" {
			t.Fatalf("token = %q, err = %v", token, err)
		}
	}
	if _, err := d.Token(context.Background(), "ada", "other-app"); err != nil {
		t.Fatal(err)
	}
	exchanges, refreshed, _ := idp.state()
	if exchanges != 2 {
		t.Fatalf("exchanges = %d, want one per audience", exchanges)
	}
	if fmt.Sprint(refreshed) != "[offline-1 offline-2]" {
		t.Fatalf("refreshed with %v, want each rotated token in turn", refreshed)
	}
}

func TestNoToken(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T) *Delegation
	}{
		{"never connected", func(t *testing.T) *Delegation { return newDelegation(newIdP(t, "ada", false)) }},
		{"the identity provider refuses the refresh", func(t *testing.T) *Delegation {
			d := newDelegation(newIdP(t, "ada", true))
			if err := connect(t, d, "ada"); err != nil {
				t.Fatal(err)
			}
			return d
		}},
		{"disconnected", func(t *testing.T) *Delegation {
			d := newDelegation(newIdP(t, "ada", false))
			if err := connect(t, d, "ada"); err != nil {
				t.Fatal(err)
			}
			if err := d.Disconnect(context.Background(), "ada"); err != nil {
				t.Fatal(err)
			}
			return d
		}},
		{"no delegation", func(*testing.T) *Delegation { return nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.prepare(t)
			if _, err := d.Token(context.Background(), "ada", "synaplan-app"); !errors.Is(err, ErrNotConnected) {
				t.Fatalf("err = %v, want ErrNotConnected", err)
			}
			if d.Connected("ada") {
				t.Fatal("still connected")
			}
		})
	}
}

func TestDisconnectRevokesTheOfflineToken(t *testing.T) {
	idp := newIdP(t, "ada", false)
	d := newDelegation(idp)
	if err := connect(t, d, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Token(context.Background(), "ada", "synaplan-app"); err != nil {
		t.Fatal(err)
	}

	if err := d.Disconnect(context.Background(), "ada"); err != nil {
		t.Fatal(err)
	}

	if _, _, revoked := idp.state(); fmt.Sprint(revoked) != "[offline-2]" {
		t.Fatalf("revoked %v, want the current, rotated token", revoked)
	}
}

func TestAFailedRevocationIsReportedAndTheTokenForgotten(t *testing.T) {
	provider := newIdP(t, "ada", false)
	provider.revokeURL = "http://127.0.0.1:1/revoke"
	d := newDelegation(provider)
	if err := connect(t, d, "ada"); err != nil {
		t.Fatal(err)
	}

	if err := d.Disconnect(context.Background(), "ada"); err == nil {
		t.Fatal("a failed revocation was not reported")
	}
	if d.Connected("ada") {
		t.Fatal("still connected")
	}
}

func TestSubject(t *testing.T) {
	cases := map[string]string{jwt("ada"): "ada", "not-a-jwt": "", "a.!!!.c": ""}
	for token, want := range cases {
		if got := Subject(token); got != want {
			t.Fatalf("Subject(%q) = %q, want %q", token, got, want)
		}
	}
}
