// Package notify checks the notifications Synaplan sends before this
// service calls back: JSON Web Tokens in compact JWS form, typed
// synaplan-notification+jwt, EdDSA (Ed25519) with a key from Synaplan's JWK
// Set, issued by the Synaplan this service talks to. The bytes match the vectors in
// Synaplan's cloud-apps contract (notify-vectors.json).
package notify

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	// OK is a notification that passed every check.
	OK = "ok"
	// BadSignature means the signature, the key, or the token is unusable.
	BadSignature = "bad_signature"
	// WrongInstance means the notification names another cloud.
	WrongInstance = "wrong_instance"
	// Stale means the token is outside its time window.
	Stale = "stale"
	// WrongIssuer means another Synaplan signed the notification.
	WrongIssuer = "wrong_issuer"
	// Replay means this id was already accepted.
	Replay = "replay"

	tokenType = "synaplan-notification+jwt"

	prefix      = "ed25519:"
	skewSeconds = 60
)

// Claims is what a notification says. Read it only after Verify said OK.
type Claims struct {
	Issuer   string `json:"iss"`
	Audience string `json:"aud"`
	Subject  string `json:"sub"`
	Type     string `json:"type"`
	ID       string `json:"id"`
	IssuedAt int64  `json:"iat"`
	Expires  int64  `json:"exp"`
}

// Parse splits a token and decodes its claims without checking anything.
func Parse(token string) (Claims, bool) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return Claims{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, false
	}
	var claims Claims
	if json.Unmarshal(raw, &claims) != nil {
		return Claims{}, false
	}
	return claims, true
}

// Precheck is the part of Verify that needs no key: the token is well
// formed, addressed to this instance and within its window. It returns OK or
// the reason, so a caller can refuse a token before fetching keys.
func Precheck(token, instanceID string, now int64) string {
	claims, ok := Parse(token)
	if !ok {
		return BadSignature
	}
	if claims.Audience != instanceID {
		return WrongInstance
	}
	if claims.Expires == 0 || now >= claims.Expires || claims.IssuedAt > now+skewSeconds {
		return Stale
	}
	return OK
}

// Verify checks the type, the EdDSA signature against keys
// (ed25519:<base64url>), the issuer, and then everything Precheck checks.
func Verify(token, issuer, instanceID string, keys []string, now int64) string {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return BadSignature
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return BadSignature
	}
	var h struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if json.Unmarshal(header, &h) != nil || h.Alg != "EdDSA" || h.Typ != tokenType {
		return BadSignature
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return BadSignature
	}
	signed := []byte(parts[0] + "." + parts[1])
	for _, key := range keys {
		pub, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(key, prefix))
		if err == nil && len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, signed, sig) {
			if claims, _ := Parse(token); issuer == "" || claims.Issuer != issuer {
				return WrongIssuer
			}
			return Precheck(token, instanceID, now)
		}
	}
	return BadSignature
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

// ErrNoKeys is returned when Synaplan's JWK Set listed no keys.
var ErrNoKeys = errors.New("no notify keys")
