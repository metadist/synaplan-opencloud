// Package notify checks the notifications Synaplan sends before this
// service acts on them: JSON Web Tokens typed synaplan-notification+jwt,
// signed with EdDSA (Ed25519) by a key from Synaplan's JWK Set and issued
// by the Synaplan this service talks to. Signature and registered claims
// are checked by golang-jwt; the type, the bounded lifetime and the replay
// list are this package's. The outcomes match the vectors in Synaplan's
// cloud-apps contract (notify-vectors.json).
package notify

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// OK is a notification that passed every check.
	OK = "ok"
	// BadSignature means the signature, the key, the type or the token is
	// unusable.
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

	skew = 60 * time.Second
	// maxLifetime bounds exp - iat, so a notification is replayable for at
	// most this long and the replay list stays small.
	maxLifetime = 10 * time.Minute
)

// Claims is what a verified notification says.
type Claims struct {
	jwt.RegisteredClaims
	Type string `json:"type"`
	ID   string `json:"id"`
}

var errWrongType = errors.New("not a synaplan notification")

// Precheck is the part of Verify that needs no key: the token is well
// formed, addressed to audience and within a window of at most maxLifetime.
// A caller refuses what fails here before fetching keys.
func Precheck(token, audience string, now time.Time) string {
	var claims Claims
	if _, _, err := jwt.NewParser().ParseUnverified(token, &claims); err != nil {
		return BadSignature
	}
	if !audienceIs(claims, audience) {
		return WrongInstance
	}
	return window(claims, now)
}

// Verify checks a notification with the key keys finds for it and returns
// its claims with OK, or the reason it was refused. An empty issuer trusts
// no one.
func Verify(token, issuer, audience string, keys jwt.Keyfunc, now time.Time) (Claims, string) {
	if issuer == "" || keys == nil {
		return Claims{}, BadSignature
	}
	var claims Claims
	_, err := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return now }),
	).ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		if t.Header["typ"] != tokenType {
			return nil, errWrongType
		}
		return keys(t)
	})
	switch {
	case err == nil:
		return claims, window(claims, now)
	case errors.Is(err, jwt.ErrTokenInvalidIssuer):
		return Claims{}, WrongIssuer
	case errors.Is(err, jwt.ErrTokenInvalidAudience):
		return Claims{}, WrongInstance
	case errors.Is(err, jwt.ErrTokenExpired), errors.Is(err, jwt.ErrTokenNotValidYet), errors.Is(err, jwt.ErrTokenRequiredClaimMissing):
		return Claims{}, Stale
	default:
		return Claims{}, BadSignature
	}
}

// window checks what the parser leaves open: an expiry, an issue time not
// ahead of now by more than skew, and a lifetime of at most maxLifetime.
// The expiry itself is exclusive, as in RFC 7519.
func window(claims Claims, now time.Time) string {
	if claims.ExpiresAt == nil || claims.IssuedAt == nil {
		return Stale
	}
	exp, iat := claims.ExpiresAt.Time, claims.IssuedAt.Time
	if !now.Before(exp) || iat.After(now.Add(skew)) || exp.Sub(iat) > maxLifetime {
		return Stale
	}
	return OK
}

func audienceIs(claims Claims, audience string) bool {
	for _, aud := range claims.Audience {
		if aud == audience {
			return true
		}
	}
	return false
}

// Seen remembers accepted notification ids until their tokens expire.
type Seen struct {
	ids map[string]time.Time
}

// NewSeen returns an empty replay list.
func NewSeen() *Seen {
	return &Seen{ids: map[string]time.Time{}}
}

// AlreadySeen reports whether type+id was accepted before and its token has
// not expired, and records it until expires when it was not.
func (s *Seen) AlreadySeen(kind, id string, now, expires time.Time) bool {
	for key, until := range s.ids {
		if !now.Before(until) {
			delete(s.ids, key)
		}
	}
	key := kind + "\x00" + id
	if _, ok := s.ids[key]; ok {
		return true
	}
	s.ids[key] = expires
	return false
}

// Forget removes kind+id from the replay list so a notification whose
// side effect could not be persisted can be retried by the sender.
func (s *Seen) Forget(kind, id string) {
	delete(s.ids, kind+"\x00"+id)
}
