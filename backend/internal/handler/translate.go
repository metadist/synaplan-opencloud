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
	"tr": {},
}

type translateRequest struct {
	ResourceID     string `json:"resourceId"`
	TargetLanguage string `json:"targetLanguage"`
}

type translateResponse struct {
	Translation string `json:"translation"`
}

// Translate reads a file from OpenCloud storage (via CS3) and asks
// Synaplan to translate it. The request uses summaryType=translation.
// A Synaplan that answered and does not list that mode is refused:
// a summary in the target language is not a translation.
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
// summary types do not include translation. An unreadable answer is
// not a refusal: the translation request is still sent.
func (h *Handler) translationUnavailable(ctx context.Context) bool {
	if h == nil || h.synaplanURL == "" {
		return false
	}
	status, body, err := h.synaplanDo(ctx, http.MethodGet, "/api/v1/config/capabilities", nil, callOptions{skipUser: true})
	if err != nil || status != http.StatusOK {
		return false
	}
	var parsed struct {
		Summary struct {
			Types []string `json:"types"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Summary.Types == nil {
		return false
	}
	for _, kind := range parsed.Summary.Types {
		if kind == "translation" {
			return false
		}
	}
	return true
}
