package handler

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

// maxCloudStatusItems is Synaplan's limit per /cloud/status request.
const maxCloudStatusItems = 500

// cloudStatusAnswer is what the extension reads for the bird on the
// files in view: whether Synaplan keeps cloud file state at all, whether
// it answered, and Synaplan's rows as it sent them.
type cloudStatusAnswer struct {
	Enabled     bool            `json:"enabled"`
	Unreachable bool            `json:"unreachable,omitempty"`
	Items       json.RawMessage `json:"items"`
}

// CloudStatus is the bird state for the files in the folder the person
// is looking at. The bird is decoration: a Synaplan without cloud file
// state answers enabled=false, a failure answers unreachable=true, and
// neither is an error the person sees.
func (h *Handler) CloudStatus(w http.ResponseWriter, r *http.Request) {
	var req synaplanapi.CloudFileStatusJSONRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	if len(req.Items) == 0 || len(req.Items) > maxCloudStatusItems {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "send between 1 and 500 files"})
		return
	}

	unreachable := cloudStatusAnswer{Enabled: true, Unreachable: true, Items: json.RawMessage("[]")}
	res, err := h.synaplanAPI.CloudFileStatusWithResponse(r.Context(), req)
	if err != nil {
		log.Printf("cloud status: %v", err)
		writeJSON(w, http.StatusOK, unreachable)
		return
	}
	switch res.StatusCode() {
	case http.StatusOK:
	case http.StatusNotFound:
		writeJSON(w, http.StatusOK, cloudStatusAnswer{Items: json.RawMessage("[]")})
		return
	default:
		log.Printf("cloud status: synaplan answered %d", res.StatusCode())
		writeJSON(w, http.StatusOK, unreachable)
		return
	}
	var parsed struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(res.Body, &parsed); err != nil || len(parsed.Items) == 0 {
		log.Printf("cloud status: unreadable answer: %v", err)
		writeJSON(w, http.StatusOK, unreachable)
		return
	}
	writeJSON(w, http.StatusOK, cloudStatusAnswer{Enabled: true, Items: parsed.Items})
}
