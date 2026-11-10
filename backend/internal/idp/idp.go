// Package idp finds the identity provider's endpoints from its issuer
// (OpenID Connect Discovery). Each endpoint can be set instead, for a
// provider without discovery or one this service reaches by another
// address than the browser; with all three set, no issuer is needed.
package idp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// retryAfter spaces discovery attempts after a failed one.
const retryAfter = 30 * time.Second

// Endpoints are where the identity provider is asked for codes and tokens
// and where tokens are revoked.
type Endpoints struct {
	Authorization string
	Token         string
	Revocation    string
}

// Provider resolves the endpoints of one issuer once, when first needed,
// so the service starts even while the identity provider is not up yet.
type Provider struct {
	issuer string
	set    Endpoints
	client *http.Client

	mu      sync.Mutex
	found   *Endpoints
	triedAt time.Time
	lastErr error
}

// New returns a Provider for issuer. Endpoints in set replace the ones the
// issuer publishes.
func New(issuer string, set Endpoints, client *http.Client) *Provider {
	return &Provider{issuer: issuer, set: set, client: client}
}

// Usable reports whether the endpoints can be known: from an issuer, or
// all three set.
func (p *Provider) Usable() bool {
	return p != nil && (p.issuer != "" || p.set.complete())
}

// TokenEndpoint is the token endpoint: the one set, without asking the
// issuer, otherwise the published one.
func (p *Provider) TokenEndpoint(ctx context.Context) (string, error) {
	if p.set.Token != "" {
		return p.set.Token, nil
	}
	endpoints, err := p.Endpoints(ctx)
	return endpoints.Token, err
}

// Endpoints returns the endpoints, the ones set taking precedence over the
// discovered ones. Discovery runs once; a failed one is returned again
// until retryAfter has passed.
func (p *Provider) Endpoints(ctx context.Context) (Endpoints, error) {
	if p.set.complete() {
		return p.set, nil
	}
	if p.issuer == "" {
		return Endpoints{}, errors.New("idp: no issuer and not every endpoint set")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.found != nil {
		return *p.found, nil
	}
	if p.lastErr != nil && time.Since(p.triedAt) < retryAfter {
		return Endpoints{}, p.lastErr
	}
	p.triedAt = time.Now()
	found, err := p.discover(ctx)
	if err != nil {
		p.lastErr = fmt.Errorf("idp: discovering %s: %w", p.issuer, err)
		return Endpoints{}, p.lastErr
	}
	p.found, p.lastErr = &found, nil
	return found, nil
}

func (p *Provider) discover(ctx context.Context) (Endpoints, error) {
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, p.client), p.issuer)
	if err != nil {
		return Endpoints{}, err
	}
	var published struct {
		Revocation string `json:"revocation_endpoint"`
	}
	if err := provider.Claims(&published); err != nil {
		return Endpoints{}, err
	}
	found := Endpoints{
		Authorization: first(p.set.Authorization, provider.Endpoint().AuthURL),
		Token:         first(p.set.Token, provider.Endpoint().TokenURL),
		Revocation:    first(p.set.Revocation, published.Revocation),
	}
	if !found.complete() {
		return Endpoints{}, errors.New("the issuer publishes no authorization, token or revocation endpoint")
	}
	return found, nil
}

func (e Endpoints) complete() bool {
	return e.Authorization != "" && e.Token != "" && e.Revocation != ""
}

func first(set, published string) string {
	if set != "" {
		return set
	}
	return published
}
