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
		PublicKey string `json:"public_key"`
		Now       int64  `json:"now"`
		Vectors   []struct {
			Name   string         `json:"name"`
			Expect string         `json:"expect"`
			Body   map[string]any `json:"body"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, vector := range doc.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			got := Verify(vector.Body, "pi_abc", []string{doc.PublicKey}, doc.Now)
			if got != vector.Expect {
				t.Fatalf("Verify() = %s, want %s", got, vector.Expect)
			}
		})
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
	cases := map[string]struct {
		body map[string]any
		want string
	}{
		"unsigned":       {map[string]any{"instance_id": "pi_abc", "ts": now}, BadSignature},
		"garbage sig":    {map[string]any{"sig": "ed25519:zz", "instance_id": "pi_abc", "ts": now}, BadSignature},
		"other instance": {map[string]any{"sig": "ed25519:" + validSig(), "instance_id": "pi_x", "ts": now}, WrongInstance},
		"too old":        {map[string]any{"sig": "ed25519:" + validSig(), "instance_id": "pi_abc", "ts": now - 1000}, Stale},
		"plausible":      {map[string]any{"sig": "ed25519:" + validSig(), "instance_id": "pi_abc", "ts": now}, OK},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Precheck(tc.body, "pi_abc", now); got != tc.want {
				t.Fatalf("Precheck() = %s, want %s", got, tc.want)
			}
		})
	}
}

// validSig is a well-formed signature of the right length; Precheck does
// not verify it, Verify would.
func validSig() string {
	return base64.RawURLEncoding.EncodeToString(make([]byte, sigBytes))
}
