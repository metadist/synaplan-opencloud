package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/delegation"
	"github.com/metadist/synaplan-opencloud/internal/synaplanauth"
)

func asPerson(r *http.Request, sub string) *http.Request {
	return r.WithContext(synaplanauth.ContextWithOIDCToken(context.Background(), tokenFor(sub)))
}

func TestAPersonAllowsAndWithdrawsWorkWhileAway(t *testing.T) {
	idp := fakeIdentityProvider(t, "ada")
	defer idp.Close()
	h := &Handler{delegation: newDelegation(idp.URL)}
	status := func() map[string]any {
		rec := httptest.NewRecorder()
		h.DelegationStatus(rec, asPerson(httptest.NewRequest(http.MethodGet, "/api/synaplan/delegation", nil), "ada"))
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body
	}
	if got := status(); got["available"] != true || got["connected"] != false || got["clientId"] != "synaplan-opencloud" || got["authorizeEndpoint"] != "https://idp/auth" {
		t.Fatalf("status before = %v", got)
	}

	rec := httptest.NewRecorder()
	h.DelegationConnect(rec, asPerson(httptest.NewRequest(http.MethodPost, "/api/synaplan/delegation", strings.NewReader(`{"code":"c","verifier":"v","redirectUri":"https://cloud/callback"}`)), "ada"))
	if rec.Code != http.StatusOK || status()["connected"] != true {
		t.Fatalf("connect: status %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	h.DelegationDisconnect(rec, asPerson(httptest.NewRequest(http.MethodDelete, "/api/synaplan/delegation", nil), "ada"))
	if rec.Code != http.StatusOK || status()["connected"] != false || len(idp.revokedTokens()) != 1 {
		t.Fatalf("disconnect: status %d, revoked %v", rec.Code, idp.revokedTokens())
	}
}

func TestDelegationConnectRefuses(t *testing.T) {
	idp := fakeIdentityProvider(t, "eve")
	defer idp.Close()
	cases := map[string]struct {
		delegation *delegation.Delegation
		body       string
		want       int
	}{
		"without organisation login": {delegation.New(delegation.Config{}, delegation.NewMemoryStore()), `{"code":"c","verifier":"v","redirectUri":"r"}`, http.StatusConflict},
		"an incomplete answer":       {newDelegation(idp.URL), `{"code":"c"}`, http.StatusBadRequest},
		"a code for someone else":    {newDelegation(idp.URL), `{"code":"c","verifier":"v","redirectUri":"r"}`, http.StatusBadRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := &Handler{delegation: tc.delegation}
			rec := httptest.NewRecorder()
			h.DelegationConnect(rec, asPerson(httptest.NewRequest(http.MethodPost, "/api/synaplan/delegation", strings.NewReader(tc.body)), "ada"))
			if rec.Code != tc.want {
				t.Fatalf("status %d %s, want %d", rec.Code, rec.Body, tc.want)
			}
			if tc.delegation.Connected("ada") {
				t.Fatal("connected anyway")
			}
		})
	}
}
