package notify

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
	for _, vector := range doc.Vectors {
		if got := Verify(vector.Token, doc.Issuer, doc.Receiver, []string{doc.PublicKey}, doc.Now); got != vector.Expect {
			t.Errorf("%s: got %s, want %s", vector.Name, got, vector.Expect)
		}
	}
	claims, ok := Parse(doc.Vectors[0].Token)
	if !ok || claims.Subject != "ada" || claims.Type != "delivery" || claims.ID != "9" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestSeenReplay(t *testing.T) {
	seen := NewSeen()
	if seen.AlreadySeen("delivery", "9", 1000) {
		t.Fatal("first sighting was treated as a replay")
	}
	if !seen.AlreadySeen("delivery", "9", 1000) {
		t.Fatal("second sighting was accepted")
	}
	if seen.AlreadySeen("delivery", "9", 1000+11*60) {
		t.Fatal("id was still remembered after ten minutes")
	}
	seen.Forget("delivery", "9")
	if seen.AlreadySeen("delivery", "9", 1000+11*60) {
		t.Fatal("a forgotten id was still treated as a replay")
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
		"not a token":     {"garbage", BadSignature},
		"other instance":  {token(map[string]any{"aud": "pi_x", "iat": now, "exp": now + 300}), WrongInstance},
		"expired":         {token(map[string]any{"aud": "pi_abc", "iat": now - 1000, "exp": now - 700}), Stale},
		"from the future": {token(map[string]any{"aud": "pi_abc", "iat": now + 600, "exp": now + 900}), Stale},
		"plausible":       {token(map[string]any{"aud": "pi_abc", "iat": now, "exp": now + 300}), OK},
		"at exp":          {token(map[string]any{"aud": "pi_abc", "iat": now - 300, "exp": now}), Stale},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Precheck(tc.token, "pi_abc", now); got != tc.want {
				t.Fatalf("Precheck() = %s, want %s", got, tc.want)
			}
		})
	}
}
