package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

// supportedLanguages is the allowlist of target language codes the
// frontend may request. Matches the list synaplan-nextcloud offers.
var supportedLanguages = map[string]struct{}{
	"en": {},
	"de": {},
	"fr": {},
	"es": {},
	"it": {},
}

type translateRequest struct {
	ResourceID     string `json:"resourceId"`
	TargetLanguage string `json:"targetLanguage"`
	// Length is optional; defaults to "long" when omitted.
	Length string `json:"length,omitempty"`
}

type translateResponse struct {
	Translation string `json:"translation"`
}

// Translate reads a file from OpenCloud storage (via CS3) and asks
// Synaplan to translate it to the requested target language.
// Implemented as /summary/generate with summaryType=abstractive,
// length=long, outputLanguage=<target>.
func (h *Handler) Translate(w http.ResponseWriter, r *http.Request) {
	var req translateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	if req.ResourceID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "resourceId is required"})
		return
	}
	if _, ok := supportedLanguages[req.TargetLanguage]; !ok {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("unsupported target language %q", req.TargetLanguage)})
		return
	}
	length := synaplanapi.Long
	if req.Length != "" {
		length = synaplanapi.PostApiSummaryGenerateJSONBodyLength(req.Length)
		if _, ok := supportedLengths[length]; !ok {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("unsupported length %q", req.Length)})
			return
		}
	}

	result, ok := h.runSummaryPipeline(w, r, req.ResourceID, "translate", func(ctx context.Context, file *cs3reader.File) (string, error) {
		text, fileID, cleanup, err := h.prepareSummaryInput(ctx, file)
		defer cleanup()
		if err != nil {
			return "", err
		}
		return h.generateSummary(
			ctx, text, fileID,
			synaplanapi.Abstractive,
			length,
			&req.TargetLanguage,
		)
	})
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, translateResponse{Translation: result})
}
