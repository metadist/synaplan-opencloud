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

// ErrBusy means other replicas kept changing the person's offline token
// while this one worked with it. The store is fine; trying again works.
var ErrBusy = errors.New("delegation: the offline token kept changing, try again")

// storeAttempts bounds a compare-and-swap against another replica.
const storeAttempts = 5

// errReadAgain means another replica changed the offline token while it was
// in use: read it again and start over.
var errReadAgain = errors.New("delegation: the offline token changed, read it again")

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
	// rotating token. Another replica is kept out by the store's lease
	// and revision.
	ops map[string]*opLock
}

type opLock struct {
	mu   sync.Mutex
	refs int
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
	return &Delegation{cfg: cfg, cache: map[string]cached{}, ops: map[string]*opLock{}}
}

// unavailable reports a store that could not be used and returns the
// error a caller sees: ErrUnavailable, wrapping err. A caller that
// cancelled its own context is not reported as a store failure.
func (d *Delegation) unavailable(ctx context.Context, err error) error {
	if !errors.Is(ctx.Err(), context.Canceled) {
		d.report(err)
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

// report hands err to StoreFailed.
func (d *Delegation) report(err error) {
	if d.cfg.StoreFailed != nil {
		d.cfg.StoreFailed(err)
	}
}

// leased runs work while subject's lease is held, on a context that ends
// before the lease could lapse.
func (d *Delegation) leased(ctx context.Context, subject string, work func(context.Context) error) error {
	held, release, err := d.cfg.Store.Lease(ctx, subject)
	if err != nil {
		return d.unavailable(ctx, err)
	}
	defer release()
	return work(held)
}

// lock takes subject's operation lock and returns its unlock. Entry is
// removed when no goroutine holds or waits on it.
func (d *Delegation) lock(subject string) func() {
	d.mu.Lock()
	op, ok := d.ops[subject]
	if !ok {
		op = &opLock{}
		d.ops[subject] = op
	}
	op.refs++
	d.mu.Unlock()
	op.mu.Lock()
	return func() {
		op.mu.Unlock()
		d.mu.Lock()
		op.refs--
		if op.refs == 0 {
			delete(d.ops, subject)
		}
		d.mu.Unlock()
	}
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
	var replaced string
	if err := d.leased(ctx, subject, func(held context.Context) error {
		var err error
		replaced, err = d.install(held, subject, resp.RefreshToken)
		return err
	}); err != nil {
		_ = d.revoke(ctx, resp.RefreshToken)
		return err
	}
	if replaced != "" {
		_ = d.revoke(ctx, replaced)
	}
	return nil
}

// Connected reports whether a readable offline token is kept for subject.
// A store that cannot be used is an error, not a no.
func (d *Delegation) Connected(ctx context.Context, subject string) (bool, error) {
	if d == nil || subject == "" {
		return false, nil
	}
	found, err := d.current(ctx, subject)
	if err != nil {
		return false, err
	}
	return found.state == readable, nil
}

// Disconnect forgets the offline token of subject and revokes it at the
// identity provider. A failed revocation is returned; the token is
// forgotten either way, so this service can no longer act for the person.
// It runs under the person's lease, so it never races a refresh.
func (d *Delegation) Disconnect(ctx context.Context, subject string) error {
	if d == nil || subject == "" {
		return nil
	}
	defer d.lock(subject)()
	var revoke string
	err := d.leased(ctx, subject, func(held context.Context) error {
		for range storeAttempts {
			found, err := d.current(held, subject)
			if err != nil || found.state == absent {
				return err
			}
			ok, err := d.cfg.Store.Delete(held, subject, found.Revision)
			if err != nil {
				return d.unavailable(held, err)
			}
			if !ok {
				continue
			}
			d.dropCache(subject)
			if found.state == readable {
				revoke = found.Token
			}
			return nil
		}
		return ErrBusy
	})
	if err != nil || revoke == "" {
		return err
	}
	return d.revoke(ctx, revoke)
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

	found, err := d.current(ctx, subject)
	if err != nil {
		return "", err
	}
	if found.state != readable {
		return "", ErrNotConnected
	}
	if token, hit := d.cachedToken(key, found.Revision); hit && !found.Rewrap {
		return token, nil
	}

	var cached string
	var result refreshResult
	err = d.leased(ctx, subject, func(held context.Context) error {
		for range storeAttempts {
			latest, err := d.current(held, subject)
			if err != nil {
				return err
			}
			if latest.state != readable {
				return ErrNotConnected
			}
			if token, hit := d.cachedToken(key, latest.Revision); hit && !latest.Rewrap {
				cached = token
				return nil
			}
			result, err = d.refresh(held, subject, latest)
			if errors.Is(err, errReadAgain) {
				continue
			}
			return err
		}
		return ErrBusy
	})
	if err != nil {
		return "", err
	}
	if cached != "" {
		return cached, nil
	}
	exchanged, err := d.tokenRequest(ctx, url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {result.accessToken},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"audience":           {audience},
	})
	if err != nil {
		return "", err
	}
	if result.cacheable {
		d.rememberAccess(key, exchanged.AccessToken, exchanged.ExpiresIn, result.revision)
	}
	return exchanged.AccessToken, nil
}

// refreshResult is an access token from a refresh. Its exchanged tokens may
// be cached for revision when cacheable; they may not when another replica
// wrote first, because then they belong to no kept revision.
type refreshResult struct {
	accessToken string
	revision    uint64
	cacheable   bool
}

// refresh redeems latest while subject's lease is held and keeps the rotated
// token, or writes the same token again when its value asks to be rewrapped.
// errReadAgain means another replica changed it while a refused token was
// being removed. Holding the lease keeps two replicas from refreshing the
// same token, which an identity provider that revokes used refresh tokens
// answers by ending the session.
func (d *Delegation) refresh(ctx context.Context, subject string, latest stored) (refreshResult, error) {
	resp, err := d.tokenRequest(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {latest.Token},
	})
	var rejected *rejectedError
	if errors.As(err, &rejected) {
		if err := d.forgetStale(ctx, subject, latest.Revision); err != nil {
			return refreshResult{}, err
		}
		return refreshResult{}, ErrNotConnected
	}
	if err != nil {
		return refreshResult{}, err
	}
	if got := Subject(resp.AccessToken); got != subject {
		return refreshResult{}, fmt.Errorf("delegation: refreshed token belongs to %q, not to %q", got, subject)
	}
	result := refreshResult{accessToken: resp.AccessToken, revision: latest.Revision, cacheable: true}
	switch {
	case resp.RefreshToken != "" && resp.RefreshToken != latest.Token:
		result.revision, result.cacheable, err = d.keep(ctx, subject, resp.RefreshToken, latest.Revision)
		if err != nil {
			return refreshResult{}, err
		}
	case latest.Rewrap:
		result.revision = d.rewrap(ctx, subject, latest.Entry)
	}
	return result, nil
}

// rewrap writes entry's token again, sealed with the current key. A failure
// is reported and changes nothing: the value still reads.
func (d *Delegation) rewrap(ctx context.Context, subject string, entry Entry) uint64 {
	rev, ok, err := d.cfg.Store.Write(ctx, subject, entry.Token, entry.Revision)
	if err != nil {
		d.report(fmt.Errorf("delegation: sealing a token with the current key: %w", err))
		return entry.Revision
	}
	if !ok {
		return entry.Revision
	}
	d.dropCache(subject)
	return rev
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

// tokenState is what the store holds for a person.
type tokenState int

const (
	absent tokenState = iota
	readable
	// unreadable is a value this replica cannot decrypt. It is replaced
	// when the person allows again, never deleted for that alone.
	unreadable
)

// stored is a person's entry and its state.
type stored struct {
	Entry
	state tokenState
}

// current reads the person's offline token. A value that cannot be read is
// reported to StoreFailed and returned as unreadable, not as an outage.
func (d *Delegation) current(ctx context.Context, subject string) (stored, error) {
	entry, ok, err := d.cfg.Store.Get(ctx, subject)
	switch {
	case errors.Is(err, ErrUnreadable):
		d.report(err)
		return stored{Entry: entry, state: unreadable}, nil
	case err != nil:
		return stored{}, d.unavailable(ctx, err)
	case !ok:
		return stored{}, nil
	}
	return stored{Entry: entry, state: readable}, nil
}

// install stores token for subject and returns the readable token it
// replaced, so the caller can revoke it after releasing the lease.
func (d *Delegation) install(ctx context.Context, subject, token string) (string, error) {
	for range storeAttempts {
		previous, err := d.current(ctx, subject)
		if err != nil {
			return "", err
		}
		if previous.state == readable && previous.Token == token && !previous.Rewrap {
			return "", nil
		}
		match := uint64(0)
		if previous.state != absent {
			match = previous.Revision
		}
		_, ok, err := d.cfg.Store.Write(ctx, subject, token, match)
		if err != nil {
			return "", d.unavailable(ctx, err)
		}
		if !ok {
			continue
		}
		d.dropCache(subject)
		if previous.state == readable && previous.Token != token {
			return previous.Token, nil
		}
		return "", nil
	}
	return "", ErrBusy
}

// keep stores a rotated token when rev is still current. cacheable is false
// when another replica wrote first: this refresh's access token is usable
// once, and must not be reused as if it belonged to the token that won.
func (d *Delegation) keep(ctx context.Context, subject, token string, rev uint64) (uint64, bool, error) {
	got, ok, err := d.cfg.Store.Write(ctx, subject, token, rev)
	if err != nil {
		return 0, false, d.unavailable(ctx, err)
	}
	d.dropCache(subject)
	if ok {
		return got, true, nil
	}
	still, err := d.current(ctx, subject)
	if err != nil {
		return 0, false, err
	}
	if still.state == absent {
		return 0, false, ErrNotConnected
	}
	return 0, false, nil
}

// forgetStale removes a token the identity provider refused, when rev is
// still current. errReadAgain means another replica replaced it.
func (d *Delegation) forgetStale(ctx context.Context, subject string, rev uint64) error {
	ok, err := d.cfg.Store.Delete(ctx, subject, rev)
	if err != nil {
		return d.unavailable(ctx, err)
	}
	if !ok {
		return errReadAgain
	}
	d.dropCache(subject)
	return nil
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
