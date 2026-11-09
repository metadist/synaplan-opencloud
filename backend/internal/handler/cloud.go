package handler

import (
	"encoding/json"
	"net/http"
	"strings"
)

// oidcCloudID is the fixed instance id Synaplan gives the one cloud it is
// configured for by environment (PLATFORM_LINKS_OIDC_CLOUD_*); notifications
// to this OpenCloud are addressed to it.
const oidcCloudID = "oidc-cloud"

// hasInstance reports whether this OpenCloud is connected to Synaplan: with
// organisation login there is nothing to register, the connection is the
// shared identity provider and Synaplan's configuration.
func (h *Handler) hasInstance() bool {
	return h.delegation.Configured()
}

// canonicalPath is the space-relative form both the read scope and the
// pickup compare: no leading "./" or "/", no trailing "/".
func canonicalPath(path string) string {
	return strings.Trim(strings.TrimPrefix(path, "./"), "/")
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	if len(body) == 0 {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeUpstream(w http.ResponseWriter, status int, body []byte) {
	var parsed struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	_ = json.Unmarshal(body, &parsed)
	sentence := parsed.Message
	if sentence == "" {
		sentence = parsed.Error
	}
	if sentence == "" || len(sentence) > 300 || strings.Contains(sentence, "Exception") {
		sentence = "Synaplan could not finish this. Try again in a minute, or ask your administrator."
	}
	// Synaplan answers either {code, error} or {error: <code>, message}.
	code := parsed.Code
	if code == "" && parsed.Message != "" && looksLikeCode(parsed.Error) {
		code = parsed.Error
	}
	if code == "" {
		code = "synaplan_error"
	}
	out := http.StatusBadGateway
	if status >= 400 && status < 500 && status != http.StatusUnauthorized {
		out = status
	}
	writeJSON(w, out, errorResponse{Error: sentence, Code: code})
}

// looksLikeCode tells a machine code such as platform_ambiguous from a
// sentence: lower-case letters, digits and underscores only.
func looksLikeCode(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}
