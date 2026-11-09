package handler

import (
	"encoding/json"
	"net/http"
)

// instanceStatus is what the Synaplan page shows about this OpenCloud in
// Synaplan. There is nothing to register: Synaplan knows the cloud from its
// own configuration (row oidc-cloud). Only a Synaplan administrator may see
// that row, so the answer is read with the signed-in person's own token.
type instanceStatus struct {
	Visible     bool   `json:"visible"`
	Known       bool   `json:"known"`
	Status      string `json:"status"`
	Host        string `json:"host"`
	DeliveryURL string `json:"deliveryUrl"`
	Reachable   *bool  `json:"reachable"`
}

// InstanceStatus reports how Synaplan sees this OpenCloud, for an
// administrator of both; anyone else gets visible=false.
func (h *Handler) InstanceStatus(w http.ResponseWriter, r *http.Request) {
	state := instanceStatus{}
	status, body, err := h.synaplanDo(r.Context(), http.MethodGet, "/api/v1/admin/platform-links/instances", nil, callOptions{})
	if err != nil || status != http.StatusOK {
		writeJSON(w, http.StatusOK, state)
		return
	}
	state.Visible = true
	var list struct {
		Instances []struct {
			ID          string `json:"id"`
			Status      string `json:"status"`
			Host        string `json:"host"`
			DeliveryURL string `json:"deliveryUrl"`
			Reachable   *bool  `json:"reachable"`
		} `json:"instances"`
	}
	_ = json.Unmarshal(body, &list)
	for _, instance := range list.Instances {
		if instance.ID == oidcCloudID {
			state.Known = true
			state.Status = instance.Status
			state.Host = instance.Host
			state.DeliveryURL = instance.DeliveryURL
			state.Reachable = instance.Reachable
		}
	}
	writeJSON(w, http.StatusOK, state)
}
