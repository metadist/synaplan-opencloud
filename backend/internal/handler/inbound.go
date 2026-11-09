package handler

import (
	"context"
	"crypto/ed25519"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/metadist/synaplan-opencloud/internal/notify"
)

// notificationAudience is the instance id Synaplan gives the one cloud it is
// configured for by environment (PLATFORM_LINKS_OIDC_CLOUD_*); notifications
// to this OpenCloud are addressed to it.
const notificationAudience = "oidc-cloud"

const (
	// keyFetchTimeout bounds one fetch of Synaplan's keys, which runs under
	// the cache lock.
	keyFetchTimeout = 10 * time.Second
	// keyCacheTTL is how long keys and issuer are trusted without asking
	// Synaplan again.
	keyCacheTTL = 10 * time.Minute
	// keyRetryAfter spaces fetches after a failed one, so requests to the
	// open inbound route cannot turn into a stream of requests to Synaplan.
	keyRetryAfter = 30 * time.Second
)

// notifications verifies what Synaplan sends: the issuer and keys it
// signs with, cached, and the ids already handled.
type notifications struct {
	mu        sync.Mutex
	seen      *notify.Seen
	loaded    bool
	fetchedAt time.Time
	triedAt   time.Time
	issuer    string
	keys      []ed25519.PublicKey
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
	issuer, keys := h.notifyKeys(r.Context())
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

// notifyKeys returns the issuer Synaplan's notifications carry and the keys
// they are signed with. Both are read through the address this service
// reaches Synaplan by, never from a URL inside a token: the issuer is
// Synaplan's public URL, which this service is not configured with. A
// successful answer is authoritative, an empty key list included, so a key
// Synaplan dropped stops being trusted; a failed one keeps the cache and
// holds off the next fetch for keyRetryAfter.
func (h *Handler) notifyKeys(ctx context.Context) (string, []ed25519.PublicKey) {
	n := &h.notifications
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.loaded && time.Since(n.fetchedAt) < keyCacheTTL || time.Since(n.triedAt) < keyRetryAfter {
		return n.issuer, n.keys
	}
	n.triedAt = time.Now()
	ctx, cancel := context.WithTimeout(anonymous(ctx), keyFetchTimeout)
	defer cancel()
	configuration, err := h.synaplanAPI.SynaplanConfigurationWithResponse(ctx)
	if err != nil || configuration.JSON200 == nil || configuration.JSON200.Issuer == "" {
		log.Printf("synaplan configuration: %v", answerOf(configuration, err))
		return n.issuer, n.keys
	}
	set, err := h.synaplanAPI.JwksWithResponse(ctx)
	if err != nil || set.JSON200 == nil {
		log.Printf("synaplan keys: %v", answerOf(set, err))
		return n.issuer, n.keys
	}
	keys := make([]ed25519.PublicKey, 0, len(set.JSON200.Keys))
	for _, jwk := range set.JSON200.Keys {
		if jwk.Kty != "OKP" || jwk.Crv != "Ed25519" {
			continue
		}
		if key, err := notify.PublicKey(jwk.X); err == nil {
			keys = append(keys, key)
		}
	}
	n.issuer = configuration.JSON200.Issuer
	n.keys = keys
	n.loaded = true
	n.fetchedAt = time.Now()
	return n.issuer, n.keys
}

// answerOf describes a failed call for the log: the transport error, or
// the status Synaplan answered with.
func answerOf(resp interface{ StatusCode() int }, err error) any {
	if err != nil {
		return err
	}
	return resp.StatusCode()
}
