package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// modelsTimeout bounds fetching the list of available chat models.
const modelsTimeout = 15 * time.Second

type chatModel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Service string `json:"service"`
}

type modelsResponse struct {
	Models []chatModel `json:"models"`
}

// Models lists the chat models Synaplan offers this person. The id is
// what Chat passes on as the model.
func (h *Handler) Models(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), modelsTimeout)
	defer cancel()
	res, err := h.synaplanAPI.GetApiConfigModelsListWithResponse(ctx, nil)
	if err != nil {
		writeFailure(w, "models", fmt.Errorf("config/models: %w", err))
		return
	}
	if res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		writeFailure(w, "models", wrapUpstreamStatus("config/models", res.StatusCode(), res.Body))
		return
	}
	out := modelsResponse{Models: []chatModel{}}
	if res.JSON200.Models != nil && res.JSON200.Models.CHAT != nil {
		for _, row := range *res.JSON200.Models.CHAT {
			if !row.Available || row.ProviderId == "" {
				continue
			}
			out.Models = append(out.Models, chatModel{ID: row.ProviderId, Name: row.Name, Service: row.Service})
		}
	}
	writeJSON(w, http.StatusOK, out)
}
