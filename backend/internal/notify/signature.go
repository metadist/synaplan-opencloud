// Package notify checks the signed notifications Synaplan sends before
// this service calls back. The bytes match the vectors in Synaplan's
// cloud-apps contract (notify-vectors.json).
package notify

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	// OK is a notification that passed every check.
	OK = "ok"
	// BadSignature means the signature, the key, or the body is unusable.
	BadSignature = "bad_signature"
	// WrongInstance means the notification names another cloud.
	WrongInstance = "wrong_instance"
	// Stale means the timestamp is outside the five-minute window.
	Stale = "stale"
	// Replay means this id was already accepted.
	Replay = "replay"

	prefix         = "ed25519:"
	windowSeconds  = 300
	publicKeyBytes = ed25519.PublicKeySize
	sigBytes       = ed25519.SignatureSize
)

// Precheck is the part of Verify that needs no key: a signature is
// present and well-formed, the instance is this one, the time is within
// the window. It returns OK or the reason, so a caller can refuse a body
// before fetching the current keys.
func Precheck(body map[string]any, instanceID string, now int64) string {
	signature, _ := body["sig"].(string)
	if signature == "" {
		return BadSignature
	}
	if _, ok := decode(signature, sigBytes); !ok {
		return BadSignature
	}
	got, _ := body["instance_id"].(string)
	if got != instanceID {
		return WrongInstance
	}
	ts, ok := unixSeconds(body["ts"])
	if !ok || abs(now-ts) > windowSeconds {
		return Stale
	}
	return OK
}

// Verify checks signature, instance and time. now is unix seconds.
// keys are the public keys from instances/self (publicKey field).
func Verify(body map[string]any, instanceID string, keys []string, now int64) string {
	if reason := Precheck(body, instanceID, now); reason != OK {
		return reason
	}
	signature, _ := body["sig"].(string)
	message, err := canonical(body)
	if err != nil {
		return BadSignature
	}
	sig, ok := decode(signature, sigBytes)
	if !ok {
		return BadSignature
	}
	for _, key := range keys {
		pub, ok := decode(key, publicKeyBytes)
		if ok && ed25519.Verify(ed25519.PublicKey(pub), message, sig) {
			return OK
		}
	}
	return BadSignature
}

func unixSeconds(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		if math.Trunc(n) != n {
			return 0, false
		}
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case int64:
		return n, true
	case int:
		return int64(n), true
	default:
		return 0, false
	}
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// canonical is the JSON Synaplan signed: sig removed, object keys sorted,
// slashes and non-ASCII left as themselves.
func canonical(body map[string]any) ([]byte, error) {
	cleaned := make(map[string]any, len(body))
	for k, v := range body {
		if k == "sig" {
			continue
		}
		cleaned[k] = v
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(sortValue(cleaned)); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func sortValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(t))
		for _, k := range keys {
			out[k] = sortValue(t[k])
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = sortValue(item)
		}
		return out
	default:
		return v
	}
}

func decode(encoded string, length int) ([]byte, bool) {
	if !strings.HasPrefix(encoded, prefix) {
		return nil, false
	}
	b64 := encoded[len(prefix):]
	raw, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(b64)
	}
	if err != nil || len(raw) != length {
		return nil, false
	}
	return raw, true
}

// Seen remembers notification ids for ten minutes.
type Seen struct {
	ids map[string]int64
}

// NewSeen returns an empty replay list.
func NewSeen() *Seen {
	return &Seen{ids: map[string]int64{}}
}

// AlreadySeen reports whether type+id was accepted in the last ten minutes
// and records it when it was not. now is unix seconds.
func (s *Seen) AlreadySeen(kind, id string, now int64) bool {
	if s == nil {
		return false
	}
	key := kind + "\x00" + id
	cutoff := now - int64((10 * time.Minute).Seconds())
	for k, at := range s.ids {
		if at < cutoff {
			delete(s.ids, k)
		}
	}
	if at, ok := s.ids[key]; ok && at >= cutoff {
		return true
	}
	s.ids[key] = now
	return false
}

// Forget releases an id so a retried notification is handled again.
// Used when the work behind it failed before anything was done.
func (s *Seen) Forget(kind, id string) {
	if s == nil {
		return
	}
	delete(s.ids, kind+"\x00"+id)
}

// ErrNoKeys is returned when instances/self listed no notify keys.
var ErrNoKeys = errors.New("no notify keys")
