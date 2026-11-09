package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// CloudStatus is the bird state for the files in the folder the person
// is looking at. A Synaplan without cloud files answers enabled=false.
func (h *Handler) CloudStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Items []struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	if len(req.Items) == 0 || len(req.Items) > 500 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Send between 1 and 500 files."})
		return
	}
	items := make([]map[string]string, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, map[string]string{"id": item.ID, "version": item.Version})
	}
	status, body, err := h.synaplanCall(r.Context(), http.MethodPost, "/api/v1/cloud/status", map[string]any{"items": items})
	if err != nil {
		log.Printf("cloud status: %v", err)
		writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "unreachable": true, "items": []any{}})
		return
	}
	if status == http.StatusNotFound {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "items": []any{}})
		return
	}
	if status != http.StatusOK {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "unreachable": true, "items": []any{}})
		return
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "unreachable": true, "items": []any{}})
		return
	}
	if _, ok := parsed["enabled"]; !ok {
		parsed["enabled"] = true
	}
	writeJSON(w, http.StatusOK, parsed)
}

// CloudDetail is the sidebar for one Synaplan file.
func (h *Handler) CloudDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("fileId")))
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "fileId is required"})
		return
	}
	status, body, err := h.synaplanCall(r.Context(), http.MethodGet, "/api/v1/cloud/files/"+strconv.Itoa(id), nil)
	if err != nil {
		log.Printf("cloud detail: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.",
			Code:  "synaplan_unreachable",
		})
		return
	}
	if status == http.StatusNotFound {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "This file is not searchable.", Code: "file_not_found"})
		return
	}
	writeRaw(w, status, body)
}
