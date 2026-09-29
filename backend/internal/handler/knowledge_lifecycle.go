package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

type knowledgeStatusResponse struct {
	InKnowledge    bool `json:"inKnowledge"`
	Stale          bool `json:"stale"`
	SynaplanFileID int  `json:"synaplanFileId"`
}

// KnowledgeStatus reports whether this OpenCloud file is already in
// the signed-in user's Synaplan knowledge base, and whether the
// stored copy is older than the file they have now.
func (h *Handler) KnowledgeStatus(w http.ResponseWriter, r *http.Request) {
	resourceID := r.URL.Query().Get("resourceId")
	if resourceID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "resourceId is required"})
		return
	}
	if h.cs3 == nil {
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not read file: storage is not configured"})
		return
	}

	file, err := h.cs3.Stat(r.Context(), resourceID)
	if err != nil {
		log.Printf("knowledge status: cs3 stat: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not read file: " + err.Error()})
		return
	}

	status, body, err := h.synaplanCall(r.Context(), http.MethodPost, "/api/v1/files/check-stale", map[string]any{
		"source": "opencloud",
		"items": []map[string]string{{
			"source_id":   resourceID,
			"source_etag": file.Etag,
		}},
	})
	if err != nil {
		log.Printf("knowledge status: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not check knowledge status: " + err.Error()})
		return
	}
	// Older Synaplan builds have no check-stale route. Treat that as
	// "not indexed yet" so Add still works.
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		writeJSON(w, http.StatusOK, knowledgeStatusResponse{})
		return
	}
	if status != http.StatusOK {
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: fmt.Sprintf("knowledge status %d: %s", status, truncate(string(body), 300)),
		})
		return
	}

	var parsed struct {
		Results []struct {
			Status string `json:"status"`
			FileID *int   `json:"file_id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || len(parsed.Results) == 0 {
		writeJSON(w, http.StatusOK, knowledgeStatusResponse{})
		return
	}
	row := parsed.Results[0]
	resp := knowledgeStatusResponse{
		InKnowledge: row.Status != "" && row.Status != "missing",
		Stale:       row.Status == "stale",
	}
	if row.FileID != nil {
		resp.SynaplanFileID = *row.FileID
	}
	writeJSON(w, http.StatusOK, resp)
}

// RemoveFromKnowledge deletes one Synaplan knowledge file (and its
// vectors) that this OpenCloud user previously added.
func (h *Handler) RemoveFromKnowledge(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "fileId"))
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "fileId is required"})
		return
	}
	status, body, err := h.synaplanCall(r.Context(), http.MethodDelete, fmt.Sprintf("/api/v1/files/%d", id), nil)
	if err != nil {
		log.Printf("knowledge remove: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not remove from knowledge: " + err.Error()})
		return
	}
	if status != http.StatusOK && status != http.StatusNoContent {
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: fmt.Sprintf("remove status %d: %s", status, truncate(string(body), 300)),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
}
