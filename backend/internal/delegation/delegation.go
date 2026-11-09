// Package delegation holds the tokens this service uses to act for a person
// while that person is away.
//
// A person connects once: the browser runs an authorization code flow with
// PKCE and offline_access for this service's confidential client, and the
// code lands here. The offline token it yields is refreshed on demand and
// exchanged (RFC 8693) for the audience a call needs. Disconnecting revokes
// it at the identity provider (RFC 7009). Nothing here can act for someone
// who did not connect. Offline tokens live in this process only.
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

	"github.com/metadist/synaplan-opencloud/internal/idp"
)

// ErrNotConnected means the person has no usable offline token: never
// connected, revoked, or expired. The person has to connect again.
var ErrNotConnected = errors.New("delegation: person is not connected")

// Config names the identity provider and this service's client.
type Config struct {
	Provider     *idp.Provider
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client
}

// Delegation obtains tokens for connected people.
type Delegation struct {
	cfg Config

	// ops serialises connecting, refreshing and disconnecting, so a rotated
	// offline token is never written back over a disconnect and two
	// refreshes never race on the same rotating token.
	ops sync.Mutex

	mu      sync.Mutex
	offline map[string]string
	cache   map[string]cached
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
}

// New returns a Delegation with no one connected.
func New(cfg Config) *Delegation {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Delegation{cfg: cfg, offline: map[string]string{}, cache: map[string]cached{}}
}

// Configured reports whether this service's client and an issuer are set
// up (organisation login). Without them, nobody can connect.
func (d *Delegation) Configured() bool {
	return d != nil && d.cfg.Provider.Issuer() != "" && d.cfg.ClientID != "" && d.cfg.ClientSecret != ""
}

// Authorization is what the browser needs to ask for a code: this
// service's client and the identity provider's authorization endpoint.
func (d *Delegation) Authorization(ctx context.Context) (clientID, endpoint string, err error) {
	endpoints, err := d.cfg.Provider.Endpoints(ctx)
	if err != nil {
		return "", "", err
	}
	return d.cfg.ClientID, endpoints.Authorization, nil
}

// Connect redeems an authorization code for an offline token and keeps it
// for subject. The token must belong to subject, so a code obtained by
// someone else cannot be attached to this person. An offline token kept
// for subject before is revoked.
func (d *Delegation) Connect(ctx context.Context, code, verifier, redirectURI, subject string) error {
	if subject == "" {
		return errors.New("delegation: no subject")
	}
	d.ops.Lock()
	defer d.ops.Unlock()
	resp, err := d.tokenRequest(ctx, url.Values{
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
		_ = d.revoke(ctx, resp.RefreshToken)
		return fmt.Errorf("delegation: token belongs to %q, not to the signed-in person", got)
	}
	previous, had := d.swap(subject, resp.RefreshToken)
	if had && previous != resp.RefreshToken {
		_ = d.revoke(ctx, previous)
	}
	return nil
}

// Connected reports whether an offline token is kept for subject.
func (d *Delegation) Connected(subject string) bool {
	if d == nil || subject == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.offline[subject]
	return ok
}

// Disconnect forgets the offline token of subject and revokes it at the
// identity provider. A failed revocation is returned; the token is
// forgotten either way, so this service can no longer act for the person.
func (d *Delegation) Disconnect(ctx context.Context, subject string) error {
	if d == nil || subject == "" {
		return nil
	}
	d.ops.Lock()
	defer d.ops.Unlock()
	previous, had := d.swap(subject, "")
	if !had {
		return nil
	}
	return d.revoke(ctx, previous)
}

// Token returns an access token for subject with the given audience. It
// refreshes the offline token and exchanges the result; a refused refresh
// forgets the offline token and reports ErrNotConnected.
func (d *Delegation) Token(ctx context.Context, subject, audience string) (string, error) {
	if d == nil {
		return "", ErrNotConnected
	}
	key := subject + "\x00" + audience
	if token, ok := d.cachedToken(key); ok {
		return token, nil
	}
	d.ops.Lock()
	defer d.ops.Unlock()
	if token, ok := d.cachedToken(key); ok {
		return token, nil
	}
	d.mu.Lock()
	offline, ok := d.offline[subject]
	d.mu.Unlock()
	if !ok {
		return "", ErrNotConnected
	}
	refreshed, err := d.tokenRequest(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {offline},
	})
	var rejected *rejectedError
	if errors.As(err, &rejected) {
		d.swap(subject, "")
		return "", ErrNotConnected
	}
	if err != nil {
		return "", err
	}
	if refreshed.RefreshToken != "" && refreshed.RefreshToken != offline {
		d.swap(subject, refreshed.RefreshToken)
	}
	exchanged, err := d.tokenRequest(ctx, url.Values{
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

func (d *Delegation) cachedToken(key string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.cache[key]
	if !ok || !time.Now().Before(c.expires) {
		return "", false
	}
	return c.token, true
}

// swap keeps offline for subject, or forgets subject when offline is
// empty, drops subject's cached access tokens, and returns what was kept
// before.
func (d *Delegation) swap(subject, offline string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	previous, had := d.offline[subject]
	if offline == "" {
		delete(d.offline, subject)
	} else {
		d.offline[subject] = offline
	}
	for key := range d.cache {
		if strings.HasPrefix(key, subject+"\x00") {
			delete(d.cache, key)
		}
	}
	return previous, had
}

type rejectedError struct{ status int }

func (e *rejectedError) Error() string {
	return fmt.Sprintf("delegation: identity provider refused the grant (%d)", e.status)
}

func (d *Delegation) tokenRequest(ctx context.Context, form url.Values) (*tokenResponse, error) {
	endpoint, err := d.cfg.Provider.TokenEndpoint(ctx)
	if err != nil {
		return nil, err
	}
	status, body, err := d.postForm(ctx, endpoint, form)
	if err != nil {
		return nil, fmt.Errorf("delegation: token endpoint: %w", err)
	}
	var parsed tokenResponse
	_ = json.Unmarshal(body, &parsed)
	if status == http.StatusBadRequest || status == http.StatusUnauthorized {
		return nil, &rejectedError{status: status}
	}
	if status != http.StatusOK || parsed.AccessToken == "" {
		return nil, fmt.Errorf("delegation: token endpoint answered %d: %s", status, parsed.Error)
	}
	return &parsed, nil
}

// revoke ends an offline token at the identity provider (RFC 7009).
func (d *Delegation) revoke(ctx context.Context, offline string) error {
	endpoints, err := d.cfg.Provider.Endpoints(ctx)
	if err != nil {
		return err
	}
	status, _, err := d.postForm(ctx, endpoints.Revocation, url.Values{
		"token":           {offline},
		"token_type_hint": {"refresh_token"},
	})
	if err != nil {
		return fmt.Errorf("delegation: revocation endpoint: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("delegation: revocation endpoint answered %d", status)
	}
	return nil
}

// postForm sends form to endpoint, authenticated as this service's client.
func (d *Delegation) postForm(ctx context.Context, endpoint string, form url.Values) (int, []byte, error) {
	form.Set("client_id", d.cfg.ClientID)
	form.Set("client_secret", d.cfg.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := d.cfg.HTTPClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return res.StatusCode, nil, fmt.Errorf("reading answer: %w", err)
	}
	return res.StatusCode, body, nil
}

func expiry(seconds int) time.Time {
	if seconds <= 0 {
		seconds = 60
	}
	return time.Now().Add(time.Duration(seconds-30) * time.Second)
}

// Subject reads the `sub` claim of a JWT without checking the signature. Use
// it only on tokens the identity provider just issued to this service.
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
