package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newDelegationHandler(t *testing.T, d *Delegated) *Handler {
	t.Helper()
	h, err := New(nil, "http://synaplan.invalid", nil, d)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func status(t *testing.T, h *Handler, userID, bearer string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.DelegationStatus(rec, httptest.NewRequestWithContext(signedIn(userID, bearer), http.MethodGet, "/api/synaplan/delegation", nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestAPersonAllowsAndWithdrawsWorkWhileAway(t *testing.T) {
	idp := fakeIdentityProvider(t, "ada")
	h := newDelegationHandler(t, delegated(t, idp, ""))
	ada := signedIn("u-ada", tokenFor("ada"))
	if _, got := status(t, h, "u-ada", tokenFor("ada")); got["available"] != true || got["connected"] != false || got["clientId"] != "synaplan-opencloud" || got["authorizeEndpoint"] != idp.URL+"/auth" {
		t.Fatalf("status before = %v", got)
	}

	rec := httptest.NewRecorder()
	h.DelegationConnect(rec, httptest.NewRequestWithContext(ada, http.MethodPost, "/api/synaplan/delegation", strings.NewReader(`{"code":"c","verifier":"v","redirectUri":"https://cloud/callback"}`)))
	if _, got := status(t, h, "u-ada", tokenFor("ada")); rec.Code != http.StatusOK || got["connected"] != true {
		t.Fatalf("connect: status %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	h.DelegationDisconnect(rec, httptest.NewRequestWithContext(ada, http.MethodDelete, "/api/synaplan/delegation", nil))
	if _, got := status(t, h, "u-ada", tokenFor("ada")); rec.Code != http.StatusOK || got["connected"] != false || len(idp.revokedTokens()) != 1 {
		t.Fatalf("disconnect: status %d, revoked %v", rec.Code, idp.revokedTokens())
	}
}

// The subject comes from the identity provider's exchange, keyed by the
// reva user OpenCloud verified, never from the bearer alone: a forged
// bearer naming someone else reaches nothing of theirs.
func TestAForgedBearerReachesNobodyElsesPermission(t *testing.T) {
	idp := fakeIdentityProvider(t, "ada")
	h := newDelegationHandler(t, delegated(t, idp, "ada"))
	eveWithAdasName := signedIn("u-eve", tokenFor("ada"))

	if code, _ := status(t, h, "u-eve", tokenFor("ada")); code != http.StatusBadGateway {
		t.Fatalf("status with a forged bearer: %d, want 502", code)
	}
	rec := httptest.NewRecorder()
	h.DelegationDisconnect(rec, httptest.NewRequestWithContext(eveWithAdasName, http.MethodDelete, "/api/synaplan/delegation", nil))
	if rec.Code != http.StatusBadGateway || !connectedAda(t, h) || len(idp.revokedTokens()) != 0 {
		t.Fatalf("disconnect with a forged bearer: status %d, ada connected %v", rec.Code, connectedAda(t, h))
	}
}

func TestDelegationConnectRefuses(t *testing.T) {
	cases := map[string]struct {
		delegated *Delegated
		body      string
		want      int
	}{
		"without organisation login": {nil, `{"code":"c","verifier":"v","redirectUri":"r"}`, http.StatusConflict},
		"an incomplete answer":       {delegated(t, fakeIdentityProvider(t, "ada"), ""), `{"code":"c"}`, http.StatusBadRequest},
		"a code for someone else":    {delegated(t, fakeIdentityProvider(t, "eve"), ""), `{"code":"c","verifier":"v","redirectUri":"r"}`, http.StatusBadRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newDelegationHandler(t, tc.delegated)
			rec := httptest.NewRecorder()
			h.DelegationConnect(rec, httptest.NewRequestWithContext(signedIn("u-ada", tokenFor("ada")), http.MethodPost, "/api/synaplan/delegation", strings.NewReader(tc.body)))
			if rec.Code != tc.want {
				t.Fatalf("status %d %s, want %d", rec.Code, rec.Body, tc.want)
			}
			if connectedAda(t, h) {
				t.Fatal("connected anyway")
			}
		})
	}
}
