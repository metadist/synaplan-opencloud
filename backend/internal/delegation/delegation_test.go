package delegation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func jwt(sub string) string {
	payload, _ := json.Marshal(map[string]string{"sub": sub})
	return "h." + base64.RawURLEncoding.EncodeToString(payload) + ".s"
}

type fakeIdP struct {
	codeSub     string
	refreshOK   bool
	exchanges   atomic.Int32
	lastGrant   string
	lastRefresh string
}

func (f *fakeIdP) serve(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("client_secret") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.lastGrant = r.Form.Get("grant_type")
		answer := map[string]any{"expires_in": 300}
		switch f.lastGrant {
		case "authorization_code":
			answer["access_token"] = jwt(f.codeSub)
			answer["refresh_token"] = "offline-1"
		case "refresh_token":
			f.lastRefresh = r.Form.Get("refresh_token")
			if !f.refreshOK {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			answer["access_token"] = jwt("ada")
			answer["refresh_token"] = "offline-2"
		case "urn:ietf:params:oauth:grant-type:token-exchange":
			f.exchanges.Add(1)
			answer["access_token"] = "for-" + r.Form.Get("audience")
		case "client_credentials":
			answer["access_token"] = "client"
		}
		_ = json.NewEncoder(w).Encode(answer)
	}))
}

func newDelegation(url string) *Delegation {
	return New(Config{TokenEndpoint: url, ClientID: "synaplan-opencloud", ClientSecret: "secret"}, NewMemoryStore())
}

func TestConnectStoresTheOfflineTokenOnlyForItsOwnSubject(t *testing.T) {
	cases := []struct {
		name      string
		codeSub   string
		connected bool
	}{
		{"own code", "ada", true},
		{"someone else's code", "bob", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idp := &fakeIdP{codeSub: tc.codeSub}
			srv := idp.serve(t)
			defer srv.Close()
			d := newDelegation(srv.URL)
			err := d.Connect(context.Background(), "code", "verifier", "https://cloud/cb", "ada")
			if (err == nil) != tc.connected || d.Connected("ada") != tc.connected {
				t.Fatalf("err = %v, connected = %v", err, d.Connected("ada"))
			}
		})
	}
}

func TestTokenRefreshesRotatesExchangesAndCaches(t *testing.T) {
	idp := &fakeIdP{codeSub: "ada", refreshOK: true}
	srv := idp.serve(t)
	defer srv.Close()
	d := newDelegation(srv.URL)
	if err := d.Connect(context.Background(), "code", "v", "https://cloud/cb", "ada"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		token, err := d.Token(context.Background(), "ada", "synaplan-app")
		if err != nil || token != "for-synaplan-app" {
			t.Fatalf("token = %q, err = %v", token, err)
		}
	}
	if idp.exchanges.Load() != 1 {
		t.Fatalf("exchanges = %d, want one cached", idp.exchanges.Load())
	}
	if _, err := d.Token(context.Background(), "ada", "opencloud"); err != nil {
		t.Fatal(err)
	}
	if idp.lastRefresh != "offline-2" {
		t.Fatalf("refresh used %q, want the rotated token", idp.lastRefresh)
	}
}

func TestARefusedRefreshDisconnects(t *testing.T) {
	idp := &fakeIdP{codeSub: "ada"}
	srv := idp.serve(t)
	defer srv.Close()
	d := newDelegation(srv.URL)
	if err := d.Connect(context.Background(), "code", "v", "https://cloud/cb", "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Token(context.Background(), "ada", "synaplan-app"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
	if d.Connected("ada") {
		t.Fatal("a revoked offline token must be forgotten")
	}
}

func TestAnUnconnectedPersonGetsNoToken(t *testing.T) {
	d := newDelegation("http://127.0.0.1:1")
	if _, err := d.Token(context.Background(), "ada", "synaplan-app"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}
	var none *Delegation
	if none.Configured() || none.Connected("ada") {
		t.Fatal("no delegation means nothing is connected")
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

func TestClientTokenIsExchangedForTheAudienceAndCached(t *testing.T) {
	idp := &fakeIdP{}
	srv := idp.serve(t)
	defer srv.Close()
	d := newDelegation(srv.URL)
	for range 2 {
		token, err := d.ClientToken(context.Background(), "synaplan-app")
		if err != nil || token != "for-synaplan-app" {
			t.Fatalf("token = %q, err = %v", token, err)
		}
	}
	if idp.exchanges.Load() != 1 {
		t.Fatalf("exchanges = %d, want one cached", idp.exchanges.Load())
	}
}
