// Package synaplanauth wires per-user authentication for every hop
// between the OpenCloud client and the Synaplan backend.
//
// Two tokens ride along with each request:
//
//   - The end-user's OIDC access token, received in the Authorization
//     header, used as the subject token in RFC 8693 token exchange
//     when the backend calls the Synaplan API on the user's behalf.
//   - The reva access token, received in the x-access-token header set
//     by OpenCloud's proxy after authenticating the OIDC session.
//     cs3reader uses it to reach the CS3 gateway as the end-user.
//
// Handlers shouldn't repeat the token-exchange dance on every call.
// Instead, this package provides:
//
//   - Middleware, which enforces both tokens plus a reva user are
//     present and stows them on the request context;
//   - NewRequestEditor, a synaplanapi RequestEditorFn that transparently
//     performs the OIDC → Synaplan exchange and sets the Bearer header
//     on every outgoing API request;
//   - CopyAuth, which lifts the OIDC token and reva user from one
//     context onto another so background cleanup work can keep talking
//     to Synaplan after the outer request ctx has been cancelled.
//
// Adding a new Synaplan-backed handler means "call the generated
// client" — authentication is already handled.
package synaplanauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"

	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

// TokenExchanger exchanges an end-user's OIDC access token for a
// Synaplan-scoped token via RFC 8693 token exchange.
type TokenExchanger interface {
	Exchange(userID, subjectToken string) (string, error)
}

type oidcTokenKey struct{}

// ContextWithOIDCToken returns a new context carrying the given OIDC
// access token. Normally set by Middleware — exposed for tests.
func ContextWithOIDCToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, oidcTokenKey{}, token)
}

// OIDCTokenFromContext returns the OIDC access token stored in the
// context, or an empty string if none was set.
func OIDCTokenFromContext(ctx context.Context) string {
	token, _ := ctx.Value(oidcTokenKey{}).(string)
	return token
}

// CopyAuth copies the auth values NewRequestEditor needs (the OIDC
// access token and the reva user) from src onto dst, returning the
// augmented dst context. Values missing on src are left unset on dst.
//
// Use this when firing a background call to Synaplan that must
// outlive the original request context — for example, a deferred
// best-effort cleanup. The outgoing synaplanapi client reads these
// values off the context, so without a copy the Exchange call would
// fail even on a fresh ctx.
func CopyAuth(src, dst context.Context) context.Context {
	if token := OIDCTokenFromContext(src); token != "" {
		dst = ContextWithOIDCToken(dst, token)
	}
	if user, ok := revactx.ContextGetUser(src); ok {
		dst = revactx.ContextSetUser(dst, user)
	}
	return dst
}

// Middleware enforces the preconditions every Synaplan-backed handler
// depends on:
//
//  1. A reva user must be present in the request context (normally put
//     there by OpenCloud's upstream auth middleware). In production
//     the OpenCloud proxy rejects unauthenticated requests before they
//     ever reach us — this is a defensive check for misconfiguration
//     and for local/test setups that bypass the proxy.
//  2. A non-empty Bearer token must be present in the Authorization
//     header. The OpenCloud proxy forwards the original OIDC access
//     token unchanged (it only adds x-access-token for the reva JWT),
//     so "Bearer <token>" here is the user's Keycloak access token.
//  3. The reva access token from the x-access-token header (set by the
//     proxy after OIDC auth) is lifted into the context via
//     revactx.ContextSetToken so anything downstream — including
//     cs3reader calling the CS3 gateway — can pull it back via
//     revactx.ContextMustGetToken without touching the request.
//
// On success both tokens are stored in the request context. On
// failure the request is rejected with 401 before the handler runs,
// so handlers can assume a reva user, a reva access token and an
// OIDC token are all present.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := revactx.ContextGetUser(r.Context()); !ok {
			http.Error(w, "unauthorized: no user in request context", http.StatusUnauthorized)
			return
		}
		oidcToken, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || oidcToken == "" {
			http.Error(w, "unauthorized: missing OIDC bearer token", http.StatusUnauthorized)
			return
		}
		revaToken := r.Header.Get(revactx.TokenHeader)
		if revaToken == "" {
			http.Error(w, "unauthorized: missing reva access token", http.StatusUnauthorized)
			return
		}
		ctx := r.Context()
		ctx = ContextWithOIDCToken(ctx, oidcToken)
		ctx = revactx.ContextSetToken(ctx, revaToken)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// NewRequestEditor returns a synaplanapi RequestEditorFn that, on every
// outgoing API request, exchanges the end-user's OIDC token for a
// Synaplan-scoped token and sets the Authorization header to it.
//
// Register once at client construction time via
// synaplanapi.WithRequestEditorFn; every call to the client will then
// be authenticated on behalf of the user whose identity and OIDC token
// are present in the request context.
func NewRequestEditor(exchanger TokenExchanger) synaplanapi.RequestEditorFn {
	return func(ctx context.Context, req *http.Request) error {
		oidcToken := OIDCTokenFromContext(ctx)
		if oidcToken == "" {
			return errors.New("synaplanauth: no OIDC token in request context (missing Middleware?)")
		}
		user, ok := revactx.ContextGetUser(ctx)
		if !ok {
			return errors.New("synaplanauth: no reva user in request context")
		}
		synaplanToken, err := exchanger.Exchange(user.GetId().GetOpaqueId(), oidcToken)
		if err != nil {
			return fmt.Errorf("synaplanauth: token exchange: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+synaplanToken)
		return nil
	}
}
