package handler

import (
	"net/http"
)

// Connection says whether this OpenCloud reaches Synaplan and whether the
// signed-in person allowed work while they are away. There is nothing to
// bind: Synaplan links the person on their first call through this cloud
// (the token names the cloud and the person).
func (h *Handler) Connection(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":       "organisation",
		"configured": h.hasInstance(),
		"bound":      h.hasInstance(),
		"background": h.canActAway(currentUserID(r.Context())),
		"serverUrl":  h.synaplanURL,
		"publicUrl":  h.settings.PublicURL,
	})
}
