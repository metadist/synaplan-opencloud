// Package synaplanauth wires per-user authentication for outgoing
// Synaplan API calls.
//
// Every call to the Synaplan API must happen on behalf of the end-user
// whose request triggered it, so we need a per-request bearer token.
// Handlers shouldn't repeat the token-exchange dance on every call;
// instead, this package provides a single RequestEditorFn that the
// generated synaplanapi client invokes transparently for every
// outgoing request.
//
// The flow:
//
//  1. Middleware pulls the user's OIDC access token out of the incoming
//     Authorization header and stores it in the request context.
//  2. The handler calls the synaplanapi client with the request context.
//  3. NewRequestEditor reads the OIDC token and the reva user from
//     context, exchanges the OIDC token for a Synaplan-scoped token
//     (RFC 8693), and sets the Bearer header on the outgoing request.
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

// Middleware extracts the Bearer token from the incoming Authorization
// header and stores it in the request context for later use by
// NewRequestEditor.
//
// The OpenCloud proxy forwards the original OIDC access token in
// Authorization unchanged (it only adds x-access-token for the reva
// JWT), so "Bearer <token>" here is the user's Keycloak access token.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && token != "" {
			r = r.WithContext(ContextWithOIDCToken(r.Context(), token))
		}
		next.ServeHTTP(w, r)
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
