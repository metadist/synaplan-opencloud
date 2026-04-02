package tokenexchange

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Exchanger performs OIDC token exchange (RFC 8693).
// It exchanges a user's OpenCloud access token for a Synaplan-scoped token
// using a confidential client. Tokens are cached per-user until near expiry.
type Exchanger struct {
	tokenEndpoint string
	clientID      string
	clientSecret  string
	audience      string
	httpClient    *http.Client

	mu    sync.RWMutex
	cache map[string]*cachedToken
}

type cachedToken struct {
	accessToken string
	expiresAt   time.Time
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// New creates a new Exchanger.
func New(tokenEndpoint, clientID, clientSecret, audience string) *Exchanger {
	return &Exchanger{
		tokenEndpoint: tokenEndpoint,
		clientID:      clientID,
		clientSecret:  clientSecret,
		audience:      audience,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		cache: make(map[string]*cachedToken),
	}
}

// Exchange performs a token exchange for the given user.
// The subjectToken is the user's OpenCloud OIDC access token.
// The userID is used as cache key.
func (e *Exchanger) Exchange(userID, subjectToken string) (string, error) {
	// Check cache first
	if token := e.getCached(userID); token != "" {
		return token, nil
	}

	// Perform token exchange
	data := url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {subjectToken},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"audience":           {e.audience},
		"client_id":          {e.clientID},
		"client_secret":      {e.clientSecret},
	}

	resp, err := e.httpClient.PostForm(e.tokenEndpoint, data)
	if err != nil {
		return "", fmt.Errorf("token exchange request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading token exchange response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token exchange returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp tokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("parsing token exchange response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("token exchange returned empty access token")
	}

	// Cache the token (with 30s buffer before expiry)
	expiresIn := tokenResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 300 // default 5 min
	}

	e.mu.Lock()
	e.cache[userID] = &cachedToken{
		accessToken: tokenResp.AccessToken,
		expiresAt:   time.Now().Add(time.Duration(expiresIn-30) * time.Second),
	}
	e.mu.Unlock()

	return tokenResp.AccessToken, nil
}

func (e *Exchanger) getCached(userID string) string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	cached, ok := e.cache[userID]
	if !ok {
		return ""
	}

	if time.Now().After(cached.expiresAt) {
		return ""
	}

	return cached.accessToken
}
