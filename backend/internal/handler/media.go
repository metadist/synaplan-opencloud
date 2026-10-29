package handler

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"path"
	"strings"
)

// Media asks Synaplan to generate an image or a video.
func (h *Handler) Media(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt string `json:"prompt"`
		Type   string `json:"type"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Describe what you want."})
		return
	}
	if req.Type != "image" && req.Type != "video" {
		req.Type = "image"
	}
	status, body, err := h.synaplanCall(r.Context(), http.MethodPost, "/api/v1/media/generate", map[string]string{
		"prompt": req.Prompt,
		"type":   req.Type,
	})
	if err != nil {
		log.Printf("media: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.", Code: "synaplan_unreachable"})
		return
	}
	if status >= 400 {
		writeUpstream(w, status, body)
		return
	}
	writeRaw(w, http.StatusOK, body)
}

// SaveMedia downloads a generated file and writes it into the person's
// Synaplan folder. The file stays in Synaplan as well.
func (h *Handler) SaveMedia(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	rel := req.URL
	if i := strings.Index(rel, "/api/v1/"); i >= 0 {
		rel = rel[i:]
	}
	if !strings.HasPrefix(rel, "/api/v1/files/") {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "That file cannot be saved."})
		return
	}
	if h.cs3 == nil {
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.", Code: "synaplan_unreachable"})
		return
	}
	status, body, err := h.synaplanDo(r.Context(), http.MethodGet, rel, nil, callOptions{limit: 100 << 20})
	if err != nil || status != http.StatusOK {
		log.Printf("media download: status %d err %v", status, err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan could not finish this. Try again in a minute, or ask your administrator.", Code: "synaplan_error"})
		return
	}
	name := path.Base(strings.TrimSpace(req.Name))
	if name == "" || name == "." || name == ".." {
		name = "synaplan.bin"
	}
	saved, err := h.cs3.Save(r.Context(), name, body)
	if err != nil {
		log.Printf("media save: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "The file was not saved. It is still in Synaplan.", Code: "synaplan_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"path":     saved,
		"sentence": "Saved to " + saved + ". The file is still in Synaplan.",
	})
}
