package handler

import (
	"context"
	"log"
	"net/http"
	"time"
)

// instanceTimeout bounds the look at Synaplan's open configuration, so a
// Synaplan that does not answer leaves the page with "not reachable"
// instead of a spinner.
const instanceTimeout = 10 * time.Second

// instanceStatus is what the Synaplan page shows about Synaplan: whether
// it answers here and what it offers clouds. There is nothing to register:
// with a shared identity provider Synaplan knows this OpenCloud from its
// own configuration and links each person on their first call. How
// Synaplan sees this OpenCloud is on Synaplan's administration, which
// tokens that come through this OpenCloud do not reach.
type instanceStatus struct {
	Reachable    bool     `json:"reachable"`
	Capabilities []string `json:"capabilities"`
}

// InstanceStatus reports Synaplan's open configuration. It is read without
// a credential; a Synaplan that does not answer is reported, not an error.
func (h *Handler) InstanceStatus(w http.ResponseWriter, r *http.Request) {
	state := instanceStatus{Capabilities: []string{}}
	ctx, cancel := context.WithTimeout(anonymous(r.Context()), instanceTimeout)
	defer cancel()
	resp, err := h.synaplanAPI.SynaplanConfigurationWithResponse(ctx)
	if err != nil || resp.JSON200 == nil {
		log.Printf("instance status: %v", answerOf(resp, err))
		writeJSON(w, http.StatusOK, state)
		return
	}
	state.Reachable = true
	if resp.JSON200.Capabilities != nil {
		state.Capabilities = resp.JSON200.Capabilities
	}
	writeJSON(w, http.StatusOK, state)
}
