// Package delegation holds the tokens this service uses to act for a person
// while that person is away, and the service's own client token.
//
// A person connects once: the browser runs a silent authorization code flow
// with offline_access for this service's confidential client, and the code
// lands here. The offline token it yields is refreshed on demand and
// exchanged (RFC 8693) for the audience a call needs: Synaplan, or OpenCloud
// itself. Nothing here can act for someone who did not connect.
package delegation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ErrNotConnected means the person has no usable offline token: never
// connected, revoked, or expired. The person has to connect again.
var ErrNotConnected = errors.New("delegation: person is not connected")

// Config names the identity provider endpoint and this service's client.
type Config struct {
	TokenEndpoint string
	ClientID      string
	ClientSecret  string
	HTTPClient    *http.Client
}

// Store keeps one offline token per person, keyed by the token subject.
type Store interface {
	Get(subject string) (string, bool)
	Put(subject, offlineToken string)
	Delete(subject string)
	Subjects() []string
}

// Delegation obtains tokens for connected people and for the service itself.
type Delegation struct {
	cfg   Config
	store Store

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	token   string
	expires time.Time
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

// New returns a Delegation backed by store.
func New(cfg Config, store Store) *Delegation {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Delegation{cfg: cfg, store: store, cache: map[string]cached{}}
}

// Configured reports whether a client is set up at all (organisation login).
func (d *Delegation) Configured() bool {
	return d != nil && d.cfg.TokenEndpoint != "" && d.cfg.ClientID != "" && d.cfg.ClientSecret != ""
}

// Connect redeems an authorization code for an offline token and stores it
// for subject. The token must belong to subject, so a code obtained by
// someone else cannot be attached to this person.
func (d *Delegation) Connect(ctx context.Context, code, verifier, redirectURI, subject string) error {
	if subject == "" {
		return errors.New("delegation: no subject")
	}
	resp, err := d.post(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	})
	if err != nil {
		return err
	}
	if resp.RefreshToken == "" {
		return errors.New("delegation: no offline token in the answer")
	}
	if got := Subject(resp.AccessToken); got != subject {
		return fmt.Errorf("delegation: token belongs to %q, not to the signed-in person", got)
	}
	d.store.Put(subject, resp.RefreshToken)
	d.forget(subject)
	return nil
}

// Connected reports whether an offline token is stored for subject.
func (d *Delegation) Connected(subject string) bool {
	if d == nil || subject == "" {
		return false
	}
	_, ok := d.store.Get(subject)
	return ok
}

// Disconnect forgets the offline token of subject.
func (d *Delegation) Disconnect(subject string) {
	if d == nil || subject == "" {
		return
	}
	d.store.Delete(subject)
	d.forget(subject)
}

// Subjects lists the people with a stored offline token.
func (d *Delegation) Subjects() []string {
	return d.store.Subjects()
}

// Token returns an access token for subject with the given audience. It
// refreshes the offline token and exchanges the result; a failed refresh
// drops the stored token and reports ErrNotConnected.
func (d *Delegation) Token(ctx context.Context, subject, audience string) (string, error) {
	if d == nil {
		return "", ErrNotConnected
	}
	key := subject + "\x00" + audience
	d.mu.Lock()
	if c, ok := d.cache[key]; ok && time.Now().Before(c.expires) {
		d.mu.Unlock()
		return c.token, nil
	}
	d.mu.Unlock()

	offline, ok := d.store.Get(subject)
	if !ok {
		return "", ErrNotConnected
	}
	refreshed, err := d.post(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {offline},
	})
	if err != nil {
		var rejected *rejectedError
		if errors.As(err, &rejected) {
			d.store.Delete(subject)
			d.forget(subject)
			return "", ErrNotConnected
		}
		return "", err
	}
	if refreshed.RefreshToken != "" && refreshed.RefreshToken != offline {
		d.store.Put(subject, refreshed.RefreshToken)
	}
	exchanged, err := d.post(ctx, url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {refreshed.AccessToken},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"audience":           {audience},
	})
	if err != nil {
		return "", err
	}
	d.mu.Lock()
	d.cache[key] = cached{token: exchanged.AccessToken, expires: expiry(exchanged.ExpiresIn)}
	d.mu.Unlock()
	return exchanged.AccessToken, nil
}

// ClientToken returns this service's own token for audience: a client
// credentials token exchanged for that audience, used only for calls the
// cloud makes as itself.
func (d *Delegation) ClientToken(ctx context.Context, audience string) (string, error) {
	if !d.Configured() {
		return "", errors.New("delegation: no client configured")
	}
	key := "\x00client\x00" + audience
	d.mu.Lock()
	if c, ok := d.cache[key]; ok && time.Now().Before(c.expires) {
		d.mu.Unlock()
		return c.token, nil
	}
	d.mu.Unlock()
	own, err := d.post(ctx, url.Values{"grant_type": {"client_credentials"}})
	if err != nil {
		return "", err
	}
	resp, err := d.post(ctx, url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {own.AccessToken},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"audience":           {audience},
	})
	if err != nil {
		return "", err
	}
	d.mu.Lock()
	d.cache[key] = cached{token: resp.AccessToken, expires: expiry(resp.ExpiresIn)}
	d.mu.Unlock()
	return resp.AccessToken, nil
}

func (d *Delegation) forget(subject string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for key := range d.cache {
		if strings.HasPrefix(key, subject+"\x00") {
			delete(d.cache, key)
		}
	}
}

type rejectedError struct{ status int }

func (e *rejectedError) Error() string {
	return fmt.Sprintf("delegation: identity provider refused the grant (%d)", e.status)
}

func (d *Delegation) post(ctx context.Context, form url.Values) (*tokenResponse, error) {
	form.Set("client_id", d.cfg.ClientID)
	form.Set("client_secret", d.cfg.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.cfg.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := d.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("delegation: token endpoint: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("delegation: reading token answer: %w", err)
	}
	var parsed tokenResponse
	_ = json.Unmarshal(body, &parsed)
	if res.StatusCode == http.StatusBadRequest || res.StatusCode == http.StatusUnauthorized {
		return nil, &rejectedError{status: res.StatusCode}
	}
	if res.StatusCode != http.StatusOK || parsed.AccessToken == "" {
		return nil, fmt.Errorf("delegation: token endpoint answered %d: %s", res.StatusCode, parsed.Error)
	}
	return &parsed, nil
}

func expiry(seconds int) time.Time {
	if seconds <= 0 {
		seconds = 60
	}
	return time.Now().Add(time.Duration(seconds-30) * time.Second)
}

// Subject reads the `sub` claim of a JWT without checking the signature. Use
// it only on tokens that were checked already, by the OpenCloud proxy or by
// the identity provider that just issued them.
func Subject(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.Sub
}

// MemoryStore keeps offline tokens in this process only.
type MemoryStore struct {
	mu     sync.RWMutex
	tokens map[string]string
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{tokens: map[string]string{}}
}

// Get returns the offline token for subject.
func (s *MemoryStore) Get(subject string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	token, ok := s.tokens[subject]
	return token, ok
}

// Put stores the offline token for subject.
func (s *MemoryStore) Put(subject, offlineToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[subject] = offlineToken
}

// Delete forgets subject.
func (s *MemoryStore) Delete(subject string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, subject)
}

// Subjects lists the stored subjects.
func (s *MemoryStore) Subjects() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.tokens))
	for subject := range s.tokens {
		out = append(out, subject)
	}
	return out
}
