package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

type summarizeRequest struct {
	ResourceID  string `json:"resourceId"`
	SummaryType string `json:"summaryType"`
	Length      string `json:"length"`
}

type summarizeResponse struct {
	Summary string `json:"summary"`
}

// Summarize reads a file from OpenCloud storage via CS3 and returns
// a Synaplan-generated summary of the requested type and length. No
// translation — outputLanguage is left unset.
func (h *Handler) Summarize(w http.ResponseWriter, r *http.Request) {
	var req summarizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	if req.ResourceID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "resourceId is required"})
		return
	}
	summaryType := synaplanapi.PostApiSummaryGenerateJSONBodySummaryType(req.SummaryType)
	if _, ok := supportedSummaryTypes[summaryType]; !ok {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("unsupported summaryType %q", req.SummaryType)})
		return
	}
	length := synaplanapi.PostApiSummaryGenerateJSONBodyLength(req.Length)
	if _, ok := supportedLengths[length]; !ok {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("unsupported length %q", req.Length)})
		return
	}

	result, ok := h.runSummaryPipeline(w, r, req.ResourceID, "summarize", func(ctx context.Context, file *cs3reader.File) (string, error) {
		text, fileID, cleanup, err := h.prepareSummaryInput(ctx, file)
		defer cleanup()
		if err != nil {
			return "", err
		}
		return h.generateSummary(ctx, text, fileID, summaryType, length, nil)
	})
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, summarizeResponse{Summary: result})
}
