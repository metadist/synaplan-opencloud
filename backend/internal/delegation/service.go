package delegation

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"time"
)

// Service obtains this service's own token with its own OIDC client (client
// credentials only). That client is not the one people's tokens are
// exchanged with, so Synaplan can tell the cloud from its people by azp.
type Service struct {
	cfg Config

	mu    sync.Mutex
	token cached
}

// NewService returns a Service for the client in cfg.
func NewService(cfg Config) *Service {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = defaultHTTPClient()
	}
	return &Service{cfg: cfg}
}

// Configured reports whether the service client is set up.
func (s *Service) Configured() bool {
	return s != nil && s.cfg.TokenEndpoint != "" && s.cfg.ClientID != "" && s.cfg.ClientSecret != ""
}

// ClientID is the service client, as announced to Synaplan.
func (s *Service) ClientID() string {
	if s == nil {
		return ""
	}
	return s.cfg.ClientID
}

// Token returns a cached client credentials token of the service client.
func (s *Service) Token(ctx context.Context) (string, error) {
	if !s.Configured() {
		return "", errors.New("delegation: no service client configured")
	}
	s.mu.Lock()
	if time.Now().Before(s.token.expires) {
		token := s.token.token
		s.mu.Unlock()
		return token, nil
	}
	s.mu.Unlock()
	resp, err := post(ctx, s.cfg, url.Values{"grant_type": {"client_credentials"}})
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.token = cached{token: resp.AccessToken, expires: expiry(resp.ExpiresIn)}
	s.mu.Unlock()
	return resp.AccessToken, nil
}
