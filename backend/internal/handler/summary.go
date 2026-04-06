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

// summaryTimeout bounds how long we wait for Synaplan on any
// summary/translate request. Individual HTTP calls inside the flow
// (upload, summary/generate) share this budget.
const summaryTimeout = 10 * time.Minute

// tempGroupKey namespaces temporary uploads we push to Synaplan only
// to have their text extracted so we can clean them up afterwards.
const tempGroupKey = "_oc_translate_temp"

// cleanupTimeout bounds the best-effort DELETE we fire to tidy up a
// temporary upload on a fresh background context.
const cleanupTimeout = 30 * time.Second

// errClientInput marks errors that should surface as 422 Unprocessable
// Entity rather than 502 Bad Gateway — the request was syntactically
// valid but the referenced file or upstream response makes the
// operation impossible (empty file, unsupported mime, Synaplan 4xx).
var errClientInput = errors.New("client input rejected")

// errorResponse is the JSON envelope the summary-backed handlers
// return on failure. Shared across /translate and /summarize so the
// frontend can treat them uniformly.
type errorResponse struct {
	Error string `json:"error"`
}

// supportedLengths bounds the /summary/generate `length` values our
// handlers accept. Excludes "custom" which requires a numeric param
// we don't surface to the frontend.
var supportedLengths = map[synaplanapi.PostApiSummaryGenerateJSONBodyLength]struct{}{
	synaplanapi.Short:  {},
	synaplanapi.Medium: {},
	synaplanapi.Long:   {},
}

// supportedSummaryTypes lists the summaryType values /summarize
// accepts — mirrors the full generated enum.
var supportedSummaryTypes = map[synaplanapi.PostApiSummaryGenerateJSONBodySummaryType]struct{}{
	synaplanapi.Abstractive:  {},
	synaplanapi.BulletPoints: {},
	synaplanapi.Extractive:   {},
}

// summaryPipelineFn is the per-handler body of work that runs with a
// CS3-opened file and returns the summary text (or translation).
type summaryPipelineFn func(ctx context.Context, file *cs3reader.File) (string, error)

// runSummaryPipeline handles the boilerplate around every summary-
// backed handler: creates the bounded ctx, opens the CS3 file, runs
// fn, and maps any error to a JSON response with the right status
// code (422 for errClientInput, 502 otherwise). Returns (result,
// true) on success or ("", false) if it has already written an
// error response. `op` is both the log prefix and the user-facing
// "<op> failed: …" error envelope prefix.
func (h *Handler) runSummaryPipeline(
	w http.ResponseWriter,
	r *http.Request,
	resourceID string,
	op string,
	fn summaryPipelineFn,
) (string, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), summaryTimeout)
	defer cancel()

	file, err := h.cs3.Open(ctx, resourceID)
	if err != nil {
		log.Printf("%s: cs3 open: %v", op, err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not read file: " + err.Error()})
		return "", false
	}
	defer func() { _ = file.Body.Close() }()

	result, err := fn(ctx, file)
	if err != nil {
		log.Printf("%s: %v", op, err)
		status := http.StatusBadGateway
		if errors.Is(err, errClientInput) {
			status = http.StatusUnprocessableEntity
		}
		writeJSON(w, status, errorResponse{Error: op + " failed: " + err.Error()})
		return "", false
	}
	return result, true
}

// isTextMime returns true for mime types we can pass straight into
// /summary/generate as raw text — no server-side extraction needed.
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

// isBinaryDocMime returns true for binary documents Synaplan can
// extract text from via its Tika pipeline (PDF, Office, ODF).
func isBinaryDocMime(mime string) bool {
	if mime == "application/pdf" || mime == "application/msword" {
		return true
	}
	return strings.HasPrefix(mime, "application/vnd.openxmlformats-officedocument") ||
		strings.HasPrefix(mime, "application/vnd.oasis.opendocument")
}

// prepareSummaryInput turns a CS3-opened file into the inputs
// generateSummary expects. For text files it reads the bytes inline
// and returns them as `text`. For binary documents it uploads to
// Synaplan with process_level=extract and returns the assigned
// `fileID`, plus a cleanup closure the caller MUST defer to delete
// the temporary upload. For unsupported mime types or empty text
// files it returns errClientInput-wrapped errors.
//
// Exactly one of (text, fileID) will be non-nil on success.
func (h *Handler) prepareSummaryInput(
	ctx context.Context,
	file *cs3reader.File,
) (text *string, fileID *int, cleanup func(), err error) {
	cleanup = func() {}

	switch {
	case isTextMime(file.MimeType):
		buf, err := io.ReadAll(file.Body)
		if err != nil {
			return nil, nil, cleanup, fmt.Errorf("read file body: %w", err)
		}
		s := string(buf)
		if strings.TrimSpace(s) == "" {
			return nil, nil, cleanup, fmt.Errorf("file is empty: %w", errClientInput)
		}
		return &s, nil, cleanup, nil

	case isBinaryDocMime(file.MimeType):
		id, err := h.uploadForExtraction(ctx, file)
		if err != nil {
			return nil, nil, cleanup, fmt.Errorf("synaplan upload: %w", err)
		}
		cleanup = func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancel()
			// synaplanauth's per-request OIDC token is bound to the
			// original ctx — hand it over to cleanupCtx so the DELETE
			// still authenticates after the request ctx is cancelled.
			cleanupCtx = synaplanauth.CopyAuth(ctx, cleanupCtx)
			if _, err := h.synaplanAPI.DeleteApiFilesDeleteWithResponse(cleanupCtx, id); err != nil {
				log.Printf("summary: cleanup delete file %d: %v", id, err)
			}
		}
		return nil, &id, cleanup, nil

	default:
		return nil, nil, cleanup, fmt.Errorf("unsupported mime type %q: %w", file.MimeType, errClientInput)
	}
}

// uploadForExtraction streams file.Body to Synaplan as a multipart
// form with process_level=extract and returns the assigned file ID.
// The body is streamed via an io.Pipe so large files don't buffer
// entirely in memory before the request goes out.
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

// generateSummary calls Synaplan's /summary/generate endpoint with
// the given parameters and returns the produced summary text. The
// caller must pass exactly one of text/fileID; outputLanguage is
// optional and drives translation when set.
func (h *Handler) generateSummary(
	ctx context.Context,
	text *string,
	fileID *int,
	summaryType synaplanapi.PostApiSummaryGenerateJSONBodySummaryType,
	length synaplanapi.PostApiSummaryGenerateJSONBodyLength,
	outputLanguage *string,
) (string, error) {
	resp, err := h.synaplanAPI.PostApiSummaryGenerateWithResponse(ctx, synaplanapi.PostApiSummaryGenerateJSONRequestBody{
		Text:           text,
		FileId:         fileID,
		SummaryType:    &summaryType,
		Length:         &length,
		OutputLanguage: outputLanguage,
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

// truncate clips long upstream error bodies before logging or
// returning them in HTTP error responses.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// wrapUpstreamStatus turns a non-200 Synaplan response into an error.
// 4xx upstream → wraps errClientInput so the top-level handler
// returns 422; 5xx → plain error → 502.
func wrapUpstreamStatus(op string, status int, body []byte) error {
	msg := fmt.Sprintf("%s status %d: %s", op, status, truncate(string(body), 300))
	if status >= 400 && status < 500 {
		return fmt.Errorf("%s: %w", msg, errClientInput)
	}
	return errors.New(msg)
}
