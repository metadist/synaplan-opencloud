package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

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
}

type translateResponse struct {
	Translation string `json:"translation"`
}

// Translate reads a file from OpenCloud storage (via CS3) and asks
// Synaplan to translate it: /summary/generate with
// summaryType=translation. A Synaplan that does not offer that type is
// refused up front; a summary in the target language is not a
// translation.
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

	if h.translationUnavailable(r.Context()) {
		writeJSON(w, http.StatusUnprocessableEntity, errorResponse{
			Error: "This Synaplan cannot translate a file. Ask your administrator.",
			Code:  "translation_unavailable",
		})
		return
	}

	result, ok := h.runSummaryPipeline(w, r, req.ResourceID, "translate", func(ctx context.Context, file *cs3reader.File) (string, error) {
		text, fileID, cleanup, err := h.prepareSummaryInput(ctx, file)
		defer cleanup()
		if err != nil {
			return "", err
		}
		return h.generateSummary(
			ctx, text, fileID,
			synaplanapi.Translation,
			synaplanapi.PostApiSummaryGenerateJSONBodyLengthLong,
			&req.TargetLanguage,
		)
	})
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, translateResponse{Translation: result})
}

// translationUnavailable is true only when Synaplan answered and its
// summary types do not include translation. No answer is not a refusal:
// the translation request is still sent and fails on its own.
func (h *Handler) translationUnavailable(ctx context.Context) bool {
	if h.synaplanAPI == nil {
		return false
	}
	res, err := h.synaplanAPI.GetApiConfigCapabilitiesWithResponse(anonymous(ctx))
	if err != nil || res.JSON200 == nil {
		return false
	}
	return !slices.Contains(res.JSON200.Summary.Types, string(synaplanapi.Translation))
}
