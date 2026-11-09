package handler

import (
	"encoding/json"
	"net/http"
)

// instanceStatus is what the Synaplan page shows about Synaplan: whether it
// answers here and what it offers clouds, from its open configuration. How
// Synaplan sees this OpenCloud is on Synaplan's own administration page;
// tokens that come through this OpenCloud do not reach admin routes.
type instanceStatus struct {
	Reachable    bool     `json:"reachable"`
	Capabilities []string `json:"capabilities"`
}

// InstanceStatus reports Synaplan's open configuration.
func (h *Handler) InstanceStatus(w http.ResponseWriter, r *http.Request) {
	state := instanceStatus{Capabilities: []string{}}
	status, body, err := h.synaplanDo(r.Context(), http.MethodGet, "/.well-known/synaplan-configuration", nil, callOptions{skipUser: true})
	if err != nil || status != http.StatusOK {
		writeJSON(w, http.StatusOK, state)
		return
	}
	var configuration struct {
		Capabilities []string `json:"capabilities"`
	}
	_ = json.Unmarshal(body, &configuration)
	state.Reachable = true
	if configuration.Capabilities != nil {
		state.Capabilities = configuration.Capabilities
	}
	writeJSON(w, http.StatusOK, state)
}
