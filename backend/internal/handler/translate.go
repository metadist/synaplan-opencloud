package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
}

type translateResponse struct {
	Translation string `json:"translation"`
}

// Translate reads a file from OpenCloud storage (via CS3) and asks
// Synaplan to translate it to the requested target language.
// Translation itself is implemented as /summary/generate with
// summaryType=abstractive, length=long, outputLanguage=<target>.
// synaplanauth.Middleware has already guaranteed the request is
// authenticated before this handler runs.
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

	ctx, cancel := context.WithTimeout(r.Context(), summaryTimeout)
	defer cancel()

	file, err := h.cs3.Open(ctx, req.ResourceID)
	if err != nil {
		log.Printf("translate: cs3 open: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not read file: " + err.Error()})
		return
	}
	defer func() { _ = file.Body.Close() }()

	translated, err := h.translateFile(ctx, file, req.TargetLanguage)
	if err != nil {
		log.Printf("translate: %v", err)
		status := http.StatusBadGateway
		if errors.Is(err, errClientInput) {
			status = http.StatusUnprocessableEntity
		}
		writeJSON(w, status, errorResponse{Error: "translation failed: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, translateResponse{Translation: translated})
}

// translateFile drives the shared summary pipeline with translate's
// fixed parameters (abstractive + long + target output language).
func (h *Handler) translateFile(ctx context.Context, file *cs3reader.File, targetLanguage string) (string, error) {
	text, fileID, cleanup, err := h.prepareSummaryInput(ctx, file)
	defer cleanup()
	if err != nil {
		return "", err
	}
	return h.generateSummary(
		ctx, text, fileID,
		synaplanapi.Abstractive,
		synaplanapi.Long,
		&targetLanguage,
	)
}
