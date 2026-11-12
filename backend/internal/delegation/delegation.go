// Package delegation holds the tokens this service uses to act for a person
// while that person is away.
//
// A person connects once: the browser runs an authorization code flow with
// PKCE and offline_access for this service's confidential client, and the
// code lands here. The offline token it yields is refreshed on demand and
// exchanged (RFC 8693) for the audience a call needs. Disconnecting revokes
// it at the identity provider (RFC 7009). Nothing here can act for someone
// who did not connect. The tokens live in a Store, one key per person:
// OpenCloud's NATS key-value store, so a restart or another replica still
// has them, or this process's memory.
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

// ErrUnavailable means the store could not be read or written. Nothing was
// kept anywhere else instead.
var ErrUnavailable = errors.New("delegation: the token store is unavailable")

// storeAttempts bounds a compare-and-swap against another replica.
const storeAttempts = 3

// Config names the identity provider and this service's client.
type Config struct {
	Provider     *idp.Provider
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client
	// Store keeps one offline token per person. It is required.
	Store Store
	// StoreFailed hears a store that could not be read or written. The
	// call fails; the token is not kept anywhere else instead.
	StoreFailed func(error)
}

// Delegation obtains tokens for connected people.
type Delegation struct {
	cfg Config

	mu    sync.Mutex
	cache map[string]cached
	// ops serialises connecting, refreshing and disconnecting per person
	// in this process, so two refreshes here never race on the same
	// rotating token. Another replica is kept out by the store's revision.
	ops map[string]*sync.Mutex
}

type cached struct {
	token    string
	expires  time.Time
	revision uint64
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
}

// New returns a Delegation. A store is read when someone connects, works,
// or stops, so a token written by another replica is visible without a
// restart of this one.
func New(cfg Config) *Delegation {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Delegation{cfg: cfg, cache: map[string]cached{}, ops: map[string]*sync.Mutex{}}
}

func (d *Delegation) storeFailed(err error) {
	if d.cfg.StoreFailed != nil {
		d.cfg.StoreFailed(err)
	}
}

// lock takes subject's operation lock and returns its unlock.
func (d *Delegation) lock(subject string) func() {
	d.mu.Lock()
	op, ok := d.ops[subject]
	if !ok {
		op = &sync.Mutex{}
		d.ops[subject] = op
	}
	d.mu.Unlock()
	op.Lock()
	return op.Unlock
}

// Configured reports whether this service's client and the identity
// provider's endpoints are set up (organisation login). Without them,
// nobody can connect.
func (d *Delegation) Configured() bool {
	return d != nil && d.cfg.Provider.Usable() && d.cfg.ClientID != "" && d.cfg.ClientSecret != ""
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
	defer d.lock(subject)()
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
	if err := d.install(ctx, subject, resp.RefreshToken); err != nil {
		_ = d.revoke(ctx, resp.RefreshToken)
		return err
	}
	return nil
}

// Connected reports whether an offline token is kept for subject. A store
// that cannot be read reports false; StoreFailed hears the error.
func (d *Delegation) Connected(ctx context.Context, subject string) bool {
	if d == nil || subject == "" {
		return false
	}
	token, _, ok, err := d.current(ctx, subject)
	return err == nil && ok && token != ""
}

// Disconnect forgets the offline token of subject and revokes it at the
// identity provider. A failed revocation is returned; the token is
// forgotten either way, so this service can no longer act for the person.
// A revision that moved is tried again, so a token another replica just
// rotated is the one that gets revoked.
func (d *Delegation) Disconnect(ctx context.Context, subject string) error {
	if d == nil || subject == "" {
		return nil
	}
	defer d.lock(subject)()
	for range storeAttempts {
		previous, rev, had, err := d.current(ctx, subject)
		if err != nil {
			return err
		}
		if !had {
			return nil
		}
		ok, err := d.cfg.Store.Delete(ctx, subject, rev)
		if err != nil {
			d.storeFailed(err)
			return fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		if !ok {
			continue
		}
		d.dropCache(subject)
		if previous == "" {
			return nil
		}
		return d.revoke(ctx, previous)
	}
	return fmt.Errorf("%w: the offline token changed while it was removed", ErrUnavailable)
}

// Token returns an access token for subject with the given audience. It
// refreshes the offline token and exchanges the result; a refused refresh
// forgets the offline token and reports ErrNotConnected. A refresh another
// replica already replaced is not forgotten: the new token is read and used.
func (d *Delegation) Token(ctx context.Context, subject, audience string) (string, error) {
	if d == nil {
		return "", ErrNotConnected
	}
	key := subject + "\x00" + audience
	defer d.lock(subject)()

	for range storeAttempts {
		offline, rev, ok, err := d.current(ctx, subject)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", ErrNotConnected
		}
		if offline == "" {
			gone, err := d.forgetStale(ctx, subject, rev)
			if err != nil {
				return "", err
			}
			if gone {
				return "", ErrNotConnected
			}
			continue
		}
		if token, hit := d.cachedToken(key, rev); hit {
			return token, nil
		}
		refreshed, err := d.tokenRequest(ctx, url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {offline},
		})
		var rejected *rejectedError
		if errors.As(err, &rejected) {
			gone, ferr := d.forgetStale(ctx, subject, rev)
			if ferr != nil {
				return "", ferr
			}
			if gone {
				return "", ErrNotConnected
			}
			continue
		}
		if err != nil {
			return "", err
		}
		if got := Subject(refreshed.AccessToken); got != subject {
			return "", fmt.Errorf("delegation: refreshed token belongs to %q, not to %q", got, subject)
		}
		cacheRev, cache := rev, true
		if refreshed.RefreshToken != "" && refreshed.RefreshToken != offline {
			cacheRev, cache, err = d.keep(ctx, subject, refreshed.RefreshToken, rev)
			if err != nil {
				return "", err
			}
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
		if cache {
			d.rememberAccess(key, exchanged.AccessToken, exchanged.ExpiresIn, cacheRev)
		}
		return exchanged.AccessToken, nil
	}
	return "", fmt.Errorf("%w: the offline token kept changing", ErrUnavailable)
}

// cachedToken is an access token exchanged from the offline token at
// revision, while it is still valid.
func (d *Delegation) cachedToken(key string, revision uint64) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.cache[key]
	if !ok || c.revision != revision || !time.Now().Before(c.expires) {
		return "", false
	}
	return c.token, true
}

func (d *Delegation) rememberAccess(key, token string, expiresIn int, revision uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cache[key] = cached{token: token, expires: expiry(expiresIn), revision: revision}
}

// current is the offline token kept for subject and its revision. An empty
// token is one the store holds but cannot read; it is replaced or forgotten.
func (d *Delegation) current(ctx context.Context, subject string) (string, uint64, bool, error) {
	token, rev, ok, err := d.cfg.Store.Get(ctx, subject)
	if err != nil {
		d.storeFailed(err)
		return "", 0, false, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return token, rev, ok, nil
}

// install stores token for subject and revokes the one it replaced.
func (d *Delegation) install(ctx context.Context, subject, token string) error {
	for range storeAttempts {
		previous, rev, had, err := d.current(ctx, subject)
		if err != nil {
			return err
		}
		match := uint64(0)
		if had {
			if previous == token {
				return nil
			}
			match = rev
		}
		if _, ok, err := d.cfg.Store.Write(ctx, subject, token, match); err != nil {
			d.storeFailed(err)
			return fmt.Errorf("%w: %w", ErrUnavailable, err)
		} else if !ok {
			continue
		}
		d.dropCache(subject)
		if had && previous != "" {
			_ = d.revoke(ctx, previous)
		}
		return nil
	}
	return fmt.Errorf("%w: the offline token changed while it was stored", ErrUnavailable)
}

// keep stores a rotated token when rev is still current. cache is false
// when another replica wrote first: this refresh's access token is usable
// once, and must not be reused as if it belonged to the token that won.
func (d *Delegation) keep(ctx context.Context, subject, token string, rev uint64) (uint64, bool, error) {
	got, ok, err := d.cfg.Store.Write(ctx, subject, token, rev)
	if err != nil {
		d.storeFailed(err)
		return 0, false, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	d.dropCache(subject)
	if ok {
		return got, true, nil
	}
	_, _, still, err := d.current(ctx, subject)
	if err != nil {
		return 0, false, err
	}
	if !still {
		return 0, false, ErrNotConnected
	}
	return 0, false, nil
}

// forgetStale removes the token when rev is still current. False means
// another replica replaced it and the caller should read again.
func (d *Delegation) forgetStale(ctx context.Context, subject string, rev uint64) (bool, error) {
	ok, err := d.cfg.Store.Delete(ctx, subject, rev)
	if err != nil {
		d.storeFailed(err)
		return false, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if ok {
		d.dropCache(subject)
	}
	return ok, nil
}

func (d *Delegation) dropCache(subject string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for key := range d.cache {
		if strings.HasPrefix(key, subject+"\x00") {
			delete(d.cache, key)
		}
	}
}

// rejectedError is the identity provider refusing the grant itself
// (invalid_grant: expired, revoked or unknown). Any other refusal, such as
// invalid_client after a changed secret, is this service's configuration
// and must not make people allow again.
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
	if status == http.StatusBadRequest && parsed.Error == "invalid_grant" {
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
