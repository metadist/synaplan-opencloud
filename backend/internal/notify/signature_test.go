package notify

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// vectors are copied from Synaplan's cloud-apps contract so a Go
// verifier and the PHP verifier accept the same bytes.
func TestVerifyVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "notify-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Issuer    string `json:"issuer"`
		Receiver  string `json:"receiver_instance_id"`
		PublicKey string `json:"public_key"`
		Now       int64  `json:"now"`
		Vectors   []struct {
			Name   string `json:"name"`
			Expect string `json:"expect"`
			Token  string `json:"token"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	key, err := PublicKey(doc.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(doc.Now, 0)
	for _, vector := range doc.Vectors {
		if _, got := Verify(vector.Token, doc.Issuer, doc.Receiver, []ed25519.PublicKey{key}, now); got != vector.Expect {
			t.Errorf("%s: got %s, want %s", vector.Name, got, vector.Expect)
		}
	}
	claims, reason := Verify(doc.Vectors[0].Token, doc.Issuer, doc.Receiver, []ed25519.PublicKey{key}, now)
	if reason != OK || claims.Subject != "ada" || claims.Type != "delivery" || claims.ID != "9" {
		t.Fatalf("claims = %+v (%s)", claims, reason)
	}
	if _, got := Verify(doc.Vectors[0].Token, "", doc.Receiver, []ed25519.PublicKey{key}, now); got != BadSignature {
		t.Fatalf("an unknown issuer was trusted: %s", got)
	}
}

func TestSeenReplay(t *testing.T) {
	seen := NewSeen()
	at := func(s int64) time.Time { return time.Unix(s, 0) }
	if seen.AlreadySeen("identity_revoked", "9", at(1000), at(1300)) {
		t.Fatal("first sighting was treated as a replay")
	}
	if !seen.AlreadySeen("identity_revoked", "9", at(1299), at(1300)) {
		t.Fatal("a replay before exp was accepted")
	}
	if seen.AlreadySeen("identity_revoked", "9", at(1300), at(1600)) {
		t.Fatal("an id was still remembered after its token expired")
	}
}

func TestPrecheckNeedsNoKey(t *testing.T) {
	now := int64(1_700_000_000)
	token := func(claims map[string]any) string {
		payload, _ := json.Marshal(claims)
		return "eyJhbGciOiJFZERTQSJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	}
	cases := map[string]struct {
		token string
		want  string
	}{
		"not a token":       {"garbage", BadSignature},
		"other instance":    {token(map[string]any{"aud": "pi_x", "iat": now, "exp": now + 300}), WrongInstance},
		"expired":           {token(map[string]any{"aud": "pi_abc", "iat": now - 1000, "exp": now - 700}), Stale},
		"from the future":   {token(map[string]any{"aud": "pi_abc", "iat": now + 600, "exp": now + 900}), Stale},
		"plausible":         {token(map[string]any{"aud": "pi_abc", "iat": now, "exp": now + 300}), OK},
		"at exp":            {token(map[string]any{"aud": "pi_abc", "iat": now - 300, "exp": now}), Stale},
		"too long a window": {token(map[string]any{"aud": "pi_abc", "iat": now, "exp": now + 3600}), Stale},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Precheck(tc.token, "pi_abc", time.Unix(now, 0)); got != tc.want {
				t.Fatalf("Precheck() = %s, want %s", got, tc.want)
			}
		})
	}
}
