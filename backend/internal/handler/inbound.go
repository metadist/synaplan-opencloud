package handler

import (
	"context"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/metadist/synaplan-opencloud/internal/notify"
)

// oidcCloudID is the instance id Synaplan gives the one cloud it is
// configured for by environment (PLATFORM_LINKS_OIDC_CLOUD_*);
// notifications to this OpenCloud are addressed to it.
const oidcCloudID = "oidc-cloud"

// keyFetchTimeout bounds the fetch of Synaplan's notification keys, which
// runs under the key cache's lock: one slow answer must not hold every
// later notification.
const keyFetchTimeout = 10 * time.Second

// keyCacheTTL is how long the keys and the issuer are trusted without
// asking Synaplan again.
const keyCacheTTL = 10 * time.Minute

// Inbound is the address Synaplan calls. Nothing is acted on until the
// notification verifies: its type, Synaplan's signature and issuer, this
// instance, its time window, and an id not seen in the last ten minutes.
// This service handles identity_revoked; other types are acknowledged and
// left to the features that need them.
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
	now := time.Now().Unix()
	// Refuse what cannot verify before touching the key cache: the checks
	// that need no key first, then the key fetch on a short deadline.
	if reason := notify.Precheck(token, oidcCloudID, now); reason != notify.OK {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: reason})
		return
	}
	keyCtx, cancel := context.WithTimeout(r.Context(), keyFetchTimeout)
	issuer, keys := h.notifyKeys(keyCtx)
	cancel()
	if reason := notify.Verify(token, issuer, oidcCloudID, keys, now); reason != notify.OK {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: reason})
		return
	}
	claims, _ := notify.Parse(token)
	if claims.Type == "" || claims.ID == "" {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: notify.BadSignature})
		return
	}
	if h.alreadySeen(claims.Type, claims.ID) {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: notify.Replay})
		return
	}
	if claims.Type == "identity_revoked" && claims.Subject != "" {
		// Synaplan ended the person's link; this service stops acting for them.
		if err := h.delegation.Disconnect(r.Context(), claims.Subject); err != nil {
			log.Printf("identity revoked: %v", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) alreadySeen(kind, id string) bool {
	h.seenMu.Lock()
	defer h.seenMu.Unlock()
	return h.seen.AlreadySeen(kind, id, time.Now().Unix())
}

// notifyKeys returns the issuer Synaplan's notifications carry and the keys
// they are signed with. Both are read through the address this service
// reaches Synaplan by, never from a URL inside a token: the issuer is
// Synaplan's public URL, which this service is not configured with, and the
// key set is read at its well-known path on that address. A 200 answer is
// authoritative, an empty key list included, so a key Synaplan dropped
// stops being trusted; a failed or unreadable answer keeps the cache.
func (h *Handler) notifyKeys(ctx context.Context) (string, []string) {
	h.keysMu.Lock()
	defer h.keysMu.Unlock()
	if h.keysLoaded && time.Since(h.keysAt) < keyCacheTTL {
		return h.issuer, h.keys
	}
	ctx = anonymous(ctx)
	configuration, err := h.synaplanAPI.SynaplanConfigurationWithResponse(ctx)
	if err != nil || configuration.JSON200 == nil || configuration.JSON200.Issuer == "" {
		log.Printf("synaplan configuration: %v", answerOf(configuration, err))
		return h.issuer, h.keys
	}
	set, err := h.synaplanAPI.JwksWithResponse(ctx)
	if err != nil || set.JSON200 == nil {
		log.Printf("synaplan keys: %v", answerOf(set, err))
		return h.issuer, h.keys
	}
	keys := make([]string, 0, len(set.JSON200.Keys))
	for _, key := range set.JSON200.Keys {
		if key.Kty == "OKP" && key.Crv == "Ed25519" && key.X != "" {
			keys = append(keys, "ed25519:"+key.X)
		}
	}
	h.issuer = configuration.JSON200.Issuer
	h.keys = keys
	h.keysLoaded = true
	h.keysAt = time.Now()
	return h.issuer, h.keys
}

// answerOf describes a failed call for the log: the transport error, or
// the status Synaplan answered with.
func answerOf(resp interface{ StatusCode() int }, err error) any {
	if err != nil {
		return err
	}
	return resp.StatusCode()
}
