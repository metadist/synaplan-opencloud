package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
	"github.com/metadist/synaplan-opencloud/internal/synaplanauth"
)

// translateTimeout bounds how long the backend will wait for Synaplan
// to return a translation. Translation via LLM can be slow — especially
// for long documents — so we need a generous ceiling. Individual HTTP
// calls inside the flow (upload, summary/generate) all share this budget.
const translateTimeout = 10 * time.Minute

// tempGroupKey namespaces temporary uploads we push to Synaplan only
// to have their text extracted. Makes them easy to identify and clean
// up later.
const tempGroupKey = "_oc_translate_temp"

// cleanupTimeout bounds the best-effort DELETE we fire when tidying
// up a temporary upload. The deferred cleanup runs on a fresh
// background context so it survives cancellation of the outer request
// ctx (e.g. client disconnects mid-response) — without this, an aborted
// translation would always leak the temp file on Synaplan's side.
const cleanupTimeout = 30 * time.Second

// errClientInput marks errors that should surface to the client as
// 422 Unprocessable Entity rather than 502 Bad Gateway. These are cases
// where the request was syntactically valid (it passed JSON decoding
// and the language allowlist) but the referenced file or upstream
// response makes the translation impossible (empty file, unsupported
// mime type, Synaplan rejecting the payload with 4xx).
var errClientInput = errors.New("client input rejected")

// translateErrorResponse is the JSON body returned on error. Using
// writeJSON instead of http.Error keeps the response shape consistent
// with the other handlers (/me) and makes it easy for the frontend to
// surface a message without sniffing the content-type.
type translateErrorResponse struct {
	Error string `json:"error"`
}

// supportedLanguages is the allowlist of target language codes the
// frontend may request. Matches the list the synaplan-nextcloud
// integration offers.
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
// Synaplan to translate it to the requested target language. For
// plain-text files we stream the bytes straight into /summary/generate
// as a "text" input. For binary documents we upload the bytes to
// /files/upload with process_level=extract and then call
// /summary/generate with the resulting "fileId" — Synaplan extracts
// the text server-side and feeds it to the summarizer in one step,
// saving a round-trip.
//
// Translation itself is implemented as summarize with
// summaryType=abstractive, length=long, outputLanguage=<target>,
// which is the convention Synaplan's API uses today.
//
// synaplanauth.Middleware has already guaranteed a reva user and an
// OIDC token are present in the context, and the generated Synaplan
// client has a request editor registered that turns the OIDC token
// into a Synaplan-scoped bearer token on every outgoing call.
func (h *Handler) Translate(w http.ResponseWriter, r *http.Request) {
	var req translateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, translateErrorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	if req.ResourceID == "" {
		writeJSON(w, http.StatusBadRequest, translateErrorResponse{Error: "resourceId is required"})
		return
	}
	if _, ok := supportedLanguages[req.TargetLanguage]; !ok {
		writeJSON(w, http.StatusBadRequest, translateErrorResponse{Error: fmt.Sprintf("unsupported target language %q", req.TargetLanguage)})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), translateTimeout)
	defer cancel()

	file, err := h.cs3.Open(ctx, req.ResourceID)
	if err != nil {
		log.Printf("translate: cs3 open: %v", err)
		writeJSON(w, http.StatusBadGateway, translateErrorResponse{Error: "could not read file: " + err.Error()})
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
		writeJSON(w, status, translateErrorResponse{Error: "translation failed: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, translateResponse{Translation: translated})
}

// translateFile dispatches to the cheap path (inline text) or the
// upload path (binary document) depending on the file's mime type.
func (h *Handler) translateFile(ctx context.Context, file *cs3reader.File, targetLanguage string) (string, error) {
	switch {
	case isTextMime(file.MimeType):
		buf, err := io.ReadAll(file.Body)
		if err != nil {
			return "", fmt.Errorf("read file body: %w", err)
		}
		text := string(buf)
		if strings.TrimSpace(text) == "" {
			return "", fmt.Errorf("file is empty: %w", errClientInput)
		}
		return h.generateTranslation(ctx, &text, nil, targetLanguage)

	case isBinaryDocMime(file.MimeType):
		fileID, err := h.uploadForExtraction(ctx, file)
		if err != nil {
			return "", fmt.Errorf("synaplan upload: %w", err)
		}
		// Best-effort cleanup of the temporary upload. Runs on a fresh
		// background context with its own short timeout so the DELETE
		// still fires when the outer request ctx was cancelled (client
		// disconnect, outer timeout). Don't fail the translation if the
		// delete fails — Synaplan can garbage-collect the temp group.
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancel()
			// synaplanauth's per-request OIDC token was bound to the
			// cancelled ctx, so hand it over to cleanupCtx explicitly.
			cleanupCtx = synaplanauth.CopyAuth(ctx, cleanupCtx)
			if _, err := h.synaplanAPI.DeleteApiFilesDeleteWithResponse(cleanupCtx, fileID); err != nil {
				log.Printf("translate: cleanup delete file %d: %v", fileID, err)
			}
		}()
		return h.generateTranslation(ctx, nil, &fileID, targetLanguage)

	default:
		return "", fmt.Errorf("unsupported mime type %q: %w", file.MimeType, errClientInput)
	}
}

// uploadForExtraction streams file.Body to Synaplan as a multipart
// form with process_level=extract and returns the assigned file ID.
//
// The multipart body is streamed via an io.Pipe so large files don't
// have to be buffered entirely in memory before the request is sent.
func (h *Handler) uploadForExtraction(ctx context.Context, file *cs3reader.File) (int, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	writeErr := make(chan error, 1)
	go func() {
		defer func() { _ = pw.Close() }()
		defer func() { _ = mw.Close() }()

		fw, err := mw.CreateFormFile("files[]", file.Name)
		if err != nil {
			writeErr <- fmt.Errorf("create form file: %w", err)
			return
		}
		if _, err := io.Copy(fw, file.Body); err != nil {
			writeErr <- fmt.Errorf("copy body: %w", err)
			return
		}
		if err := mw.WriteField("group_key", tempGroupKey); err != nil {
			writeErr <- fmt.Errorf("write group_key: %w", err)
			return
		}
		if err := mw.WriteField("process_level", "extract"); err != nil {
			writeErr <- fmt.Errorf("write process_level: %w", err)
			return
		}
		writeErr <- nil
	}()

	resp, err := h.synaplanAPI.PostApiFilesUploadWithBodyWithResponse(ctx, mw.FormDataContentType(), pr)
	if err != nil {
		return 0, fmt.Errorf("upload request: %w", err)
	}
	if err := <-writeErr; err != nil {
		return 0, err
	}
	if resp.StatusCode() != http.StatusOK {
		return 0, wrapUpstreamStatus("upload", resp.StatusCode(), resp.Body)
	}

	var parsed struct {
		Success bool `json:"success"`
		Files   []struct {
			ID int `json:"id"`
		} `json:"files"`
	}
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		return 0, fmt.Errorf("parse upload response: %w", err)
	}
	if !parsed.Success || len(parsed.Files) == 0 {
		return 0, fmt.Errorf("upload returned no file id (body: %s)", truncate(string(resp.Body), 300))
	}
	return parsed.Files[0].ID, nil
}

// generateTranslation calls Synaplan's summary/generate endpoint with
// parameters that repurpose the summarizer as a translator (abstractive
// summary type + long length + target output language) using either a
// raw text string or a previously-uploaded fileId as the input. The
// caller must pass exactly one of text/fileID; the other must be nil.
func (h *Handler) generateTranslation(ctx context.Context, text *string, fileID *int, targetLanguage string) (string, error) {
	summaryType := synaplanapi.Abstractive
	length := synaplanapi.Long

	resp, err := h.synaplanAPI.PostApiSummaryGenerateWithResponse(ctx, synaplanapi.PostApiSummaryGenerateJSONRequestBody{
		Text:           text,
		FileId:         fileID,
		SummaryType:    &summaryType,
		Length:         &length,
		OutputLanguage: &targetLanguage,
	})
	if err != nil {
		return "", fmt.Errorf("summary/generate: %w", err)
	}
	if resp.StatusCode() != http.StatusOK {
		return "", wrapUpstreamStatus("summary/generate", resp.StatusCode(), resp.Body)
	}

	var parsed struct {
		Success bool   `json:"success"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		return "", fmt.Errorf("parse summary response: %w", err)
	}
	if !parsed.Success {
		return "", errors.New("synaplan returned success=false for summary/generate")
	}
	return parsed.Summary, nil
}

// isTextMime returns true if the mime type represents plain text that
// can be read directly from CS3 and passed to /summary/generate as raw
// input — no server-side extraction needed.
func isTextMime(mime string) bool {
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	switch mime {
	case "application/json", "application/xml", "application/rtf":
		return true
	}
	return false
}

// isBinaryDocMime returns true if the mime type is a binary document
// Synaplan can extract text from via its Tika pipeline (PDF, Office,
// ODF).
func isBinaryDocMime(mime string) bool {
	if mime == "application/pdf" || mime == "application/msword" {
		return true
	}
	return strings.HasPrefix(mime, "application/vnd.openxmlformats-officedocument") ||
		strings.HasPrefix(mime, "application/vnd.oasis.opendocument")
}

// truncate clips long upstream error bodies before logging or
// returning them in HTTP error responses.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// wrapUpstreamStatus turns a non-200 Synaplan response into an error
// whose wrapped sentinel tells the top-level handler which HTTP status
// to return. 4xx upstream responses mean the user-supplied input was
// rejected (wrong format, too big, unsupported payload) and propagate
// as errClientInput → 422. 5xx upstream responses mean Synaplan itself
// is unhappy and propagate as a plain error → 502.
func wrapUpstreamStatus(op string, status int, body []byte) error {
	msg := fmt.Sprintf("%s status %d: %s", op, status, truncate(string(body), 300))
	if status >= 400 && status < 500 {
		return fmt.Errorf("%s: %w", msg, errClientInput)
	}
	return errors.New(msg)
}
