package handler

import "net/http"

// The ways this service signs in to Synaplan, as the settings page names
// them.
const (
	// loginPerson: each person's OpenCloud login is their Synaplan login.
	loginPerson = "person"
	// loginSharedKey: one Synaplan account for everyone (Mode B).
	loginSharedKey = "shared-key"
)

// Connection tells the settings page how this OpenCloud signs in to
// Synaplan. There is nothing for a person to bind: with a shared identity
// provider Synaplan links them on their first call through this cloud.
func (h *Handler) Connection(w http.ResponseWriter, _ *http.Request) {
	login := loginSharedKey
	if h.exchanger != nil {
		login = loginPerson
	}
	writeJSON(w, http.StatusOK, map[string]any{"login": login})
}
