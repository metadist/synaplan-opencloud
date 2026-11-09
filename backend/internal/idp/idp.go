// Package idp finds the identity provider's endpoints from its issuer
// (OpenID Connect Discovery). The token endpoint can be overridden for a
// service that reaches the provider by another address than the browser.
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
	issuer        string
	tokenOverride string
	client        *http.Client

	mu      sync.Mutex
	found   *Endpoints
	triedAt time.Time
	lastErr error
}

// New returns a Provider for issuer. tokenOverride, when set, replaces the
// token endpoint the issuer publishes.
func New(issuer, tokenOverride string, client *http.Client) *Provider {
	return &Provider{issuer: issuer, tokenOverride: tokenOverride, client: client}
}

// Issuer is the issuer this Provider was created for; empty when none is
// configured.
func (p *Provider) Issuer() string {
	if p == nil {
		return ""
	}
	return p.issuer
}

// TokenEndpoint is the token endpoint: the override when set, without
// asking the issuer, otherwise the published one.
func (p *Provider) TokenEndpoint(ctx context.Context) (string, error) {
	if p.tokenOverride != "" {
		return p.tokenOverride, nil
	}
	endpoints, err := p.Endpoints(ctx)
	return endpoints.Token, err
}

// Endpoints returns the issuer's endpoints, discovered once. A failed
// discovery is returned again until retryAfter has passed.
func (p *Provider) Endpoints(ctx context.Context) (Endpoints, error) {
	if p.issuer == "" {
		return Endpoints{}, errors.New("idp: no issuer configured")
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
		Authorization: provider.Endpoint().AuthURL,
		Token:         provider.Endpoint().TokenURL,
		Revocation:    published.Revocation,
	}
	if p.tokenOverride != "" {
		found.Token = p.tokenOverride
	}
	if found.Authorization == "" || found.Token == "" || found.Revocation == "" {
		return Endpoints{}, errors.New("the issuer publishes no authorization, token or revocation endpoint")
	}
	return found, nil
}
