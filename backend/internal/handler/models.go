package handler

import (
	"fmt"
	"log"
	"net/http"
)

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
	res, err := h.synaplanAPI.GetApiConfigModelsListWithResponse(r.Context(), nil)
	if err != nil || res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		if err == nil {
			err = fmt.Errorf("config/models status %d: %s", res.StatusCode(), truncate(string(res.Body), 300))
		}
		log.Printf("models: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "Synaplan could not finish this. Try again in a minute, or ask your administrator.",
			Code:  "synaplan_error",
		})
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
