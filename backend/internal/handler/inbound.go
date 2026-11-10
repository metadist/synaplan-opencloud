package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/MicahParks/keyfunc/v2"
	"github.com/golang-jwt/jwt/v5"

	"github.com/metadist/synaplan-opencloud/internal/notify"
)

// notificationAudience is the instance id Synaplan gives the one cloud it is
// configured for by environment (PLATFORM_LINKS_OIDC_CLOUD_*); notifications
// to this OpenCloud are addressed to it.
const notificationAudience = "oidc-cloud"

const (
	// discoveryTimeout bounds reading Synaplan's configuration and first
	// key set, which runs under the lock.
	discoveryTimeout = 10 * time.Second
	// discoveryRetryAfter spaces attempts after a failed one, so requests to
	// the open inbound route cannot turn into a stream of requests to
	// Synaplan.
	discoveryRetryAfter = 30 * time.Second
)

// notifications verifies what Synaplan sends: the issuer it names, the
// key set it signs with (refreshed by keyfunc hourly and when an unknown
// key id shows up, at most once a minute), and the ids already handled.
type notifications struct {
	mu      sync.Mutex
	seen    *notify.Seen
	issuer  string
	keys    *keyfunc.JWKS
	triedAt time.Time
}

// Inbound is the address Synaplan calls. Nothing is acted on until the
// notification verifies: its type, Synaplan's signature and issuer, this
// instance, its time window, and an id not handled before. This service
// acts on identity_revoked; other types are acknowledged and left to the
// features that need them.
func (h *Handler) Inbound(w http.ResponseWriter, r *http.Request) {
	if !h.delegation.Configured() {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not_configured"})
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: notify.BadSignature})
		return
	}
	token := string(raw)
	now := time.Now()
	if reason := notify.Precheck(token, notificationAudience, now); reason != notify.OK {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: reason})
		return
	}
	issuer, keys := h.synaplanSigner(r.Context())
	claims, reason := notify.Verify(token, issuer, notificationAudience, keys, now)
	if reason != notify.OK {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: reason})
		return
	}
	if claims.Type == "" || claims.ID == "" {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: notify.BadSignature})
		return
	}
	if h.alreadyHandled(claims, now) {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: notify.Replay})
		return
	}
	if claims.Type == "identity_revoked" && claims.Subject != "" {
		if err := h.delegation.Disconnect(r.Context(), claims.Subject); err != nil {
			log.Printf("identity revoked: %v", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) alreadyHandled(claims notify.Claims, now time.Time) bool {
	h.notifications.mu.Lock()
	defer h.notifications.mu.Unlock()
	return h.notifications.seen.AlreadySeen(claims.Type, claims.ID, now, claims.ExpiresAt.Time)
}

// synaplanSigner returns the issuer Synaplan's notifications carry and the
// key function for its key set. Both come from the address this service
// reaches Synaplan by, never from a URL inside a token: the issuer is
// Synaplan's public URL, which this service is not configured with. Until
// both are known, nothing verifies.
func (h *Handler) synaplanSigner(ctx context.Context) (string, jwt.Keyfunc) {
	n := &h.notifications
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.keys != nil {
		return n.issuer, n.keys.Keyfunc
	}
	if time.Since(n.triedAt) < discoveryRetryAfter {
		return "", nil
	}
	n.triedAt = time.Now()
	ctx, cancel := context.WithTimeout(anonymous(ctx), discoveryTimeout)
	defer cancel()
	// Open document, like the key set below: no credential, read directly.
	issuer, err := h.synaplanIssuer(ctx)
	if err != nil {
		log.Printf("synaplan configuration: %v", err)
		return "", nil
	}
	keys, err := keyfunc.Get(strings.TrimRight(h.synaplanURL, "/")+"/.well-known/jwks.json", keyfunc.Options{
		Ctx:               context.WithoutCancel(ctx),
		Client:            h.httpClient,
		RefreshInterval:   time.Hour,
		RefreshRateLimit:  time.Minute,
		RefreshTimeout:    discoveryTimeout,
		RefreshUnknownKID: true,
		RefreshErrorHandler: func(err error) {
			log.Printf("synaplan keys: %v", err)
		},
	})
	if err != nil {
		log.Printf("synaplan keys: %v", err)
		return "", nil
	}
	n.issuer, n.keys = issuer, keys
	return n.issuer, n.keys.Keyfunc
}

// synaplanIssuer reads the issuer Synaplan's notifications carry from its
// open configuration document at SYNAPLAN_URL.
func (h *Handler) synaplanIssuer(ctx context.Context) (string, error) {
	endpoint, err := url.JoinPath(h.synaplanURL, ".well-known/synaplan-configuration")
	if err != nil {
		return "", fmt.Errorf("building configuration url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("building configuration request: %w", err)
	}
	res, err := h.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if err != nil {
		return "", fmt.Errorf("reading configuration: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("configuration answered %d", res.StatusCode)
	}
	var doc struct {
		Issuer string `json:"issuer"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || doc.Issuer == "" {
		return "", fmt.Errorf("configuration names no issuer")
	}
	return doc.Issuer, nil
}
