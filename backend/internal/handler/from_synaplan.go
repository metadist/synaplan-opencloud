package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

// defaultMaxCopyBytes is Cloudflare's limit for a body on the public
// edge, 100 MiB. Streaming the Synaplan download into the CS3 upload
// would avoid holding the file in this service: the simple upload
// needs the length up front, which the download's Content-Length can
// give. Until then the bytes are buffered. A film does not fit in
// 100 MiB. SYNAPLAN_MAX_COPY_BYTES raises the cap where the edge in
// front of this service allows a larger body; the buffer, and the
// memory the process needs, grow with that cap.
const defaultMaxCopyBytes int64 = 100 << 20

// copyDownloadTimeout bounds reading a file from Synaplan and saving it
// into OpenCloud.
const copyDownloadTimeout = 5 * time.Minute

var (
	// errSynaplanUnreachable marks a transport failure talking to Synaplan
	// (connection refused, timeout, or dropped body stream), distinct from
	// an HTTP error status Synaplan answered with.
	errSynaplanUnreachable = errors.New("synaplan unreachable")
	// errCopyTooLarge marks a downloaded file exceeding copyLimit().
	errCopyTooLarge = errors.New("file exceeds copy limit")
)

func unreachableFailure() errorResponse {
	return errorResponse{
		Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.",
		Code:  "synaplan_unreachable",
	}
}

func tooLargeFailure() errorResponse {
	return errorResponse{
		Error: "This file is too large to copy here.",
		Code:  "file_too_large",
	}
}

func copyFailedFailure() errorResponse {
	return errorResponse{
		Error: "The file was not copied. It is still in Synaplan.",
		Code:  "copy_failed",
	}
}

// SetMaxCopyBytes sets the copy cap from configuration. Zero keeps
// the default.
func (h *Handler) SetMaxCopyBytes(n int64) {
	if n > 0 {
		h.maxCopyBytes = n
	}
}

// refuseSharedKey reports that From Synaplan is closed. With one
// shared key the list is the key owner's files, and any signed-in
// OpenCloud user could read and copy them.
func (h *Handler) refuseSharedKey(w http.ResponseWriter) bool {
	if h == nil || !h.sharedKey {
		return false
	}
	writeJSON(w, http.StatusForbidden, errorResponse{
		Error: "From Synaplan needs each person signed in to Synaplan. This installation uses one shared key.",
		Code:  "shared_key",
	})
	return true
}

func (h *Handler) copyLimit() int64 {
	if h != nil && h.maxCopyBytes > 0 {
		return h.maxCopyBytes
	}
	return defaultMaxCopyBytes
}

// readCappedSynaplanResponse reads at most h.copyLimit() bytes from a
// Synaplan file response, distinguishing transport failures from HTTP
// status failures even when an error body is truncated.
func (h *Handler) readCappedSynaplanResponse(op string, resp *http.Response, reqErr error) ([]byte, error) {
	if reqErr != nil {
		return nil, fmt.Errorf("%s: %w: %w", op, errSynaplanUnreachable, reqErr)
	}
	defer func() { _ = resp.Body.Close() }()
	limit := h.copyLimit()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if resp.StatusCode != http.StatusOK {
		return nil, wrapUpstreamStatus(op, resp.StatusCode, body)
	}
	if readErr != nil {
		return nil, fmt.Errorf("%s: %w: %w", op, errSynaplanUnreachable, readErr)
	}
	if int64(len(body)) > limit {
		return nil, errCopyTooLarge
	}
	return body, nil
}

// writeCopyFetchFailure logs err and writes the matching HTTP response
// (413 for oversized files, 502 synaplan_unreachable for transport
// failures, 502 synaplan_error for upstream HTTP failures).
func writeCopyFetchFailure(w http.ResponseWriter, logPrefix string, err error) {
	if errors.Is(err, errCopyTooLarge) {
		writeJSON(w, http.StatusRequestEntityTooLarge, tooLargeFailure())
		return
	}
	log.Printf("%s: %v", logPrefix, err)
	if errors.Is(err, errSynaplanUnreachable) {
		writeJSON(w, http.StatusBadGateway, unreachableFailure())
		return
	}
	writeJSON(w, http.StatusBadGateway, publicFailure(err))
}

// synaplanFilesPerPage is how many of the person's Synaplan files one
// page of "From Synaplan" lists.
const synaplanFilesPerPage = 50

// ListSynaplanFiles is one page of the person's own files in Synaplan,
// newest first, for "From Synaplan".
func (h *Handler) ListSynaplanFiles(w http.ResponseWriter, r *http.Request) {
	if h.refuseSharedKey(w) {
		return
	}
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	limit := synaplanFilesPerPage
	res, err := h.synaplanAPI.GetApiFilesListWithResponse(r.Context(), &synaplanapi.GetApiFilesListParams{
		Page:  &page,
		Limit: &limit,
	})
	if err != nil {
		log.Printf("from synaplan: list: %v", err)
		writeJSON(w, http.StatusBadGateway, unreachableFailure())
		return
	}
	if res.StatusCode() != http.StatusOK {
		log.Printf("from synaplan: list answered %d: %s", res.StatusCode(), truncate(string(res.Body), 300))
		writeJSON(w, http.StatusBadGateway, publicFailure(nil))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(res.Body)
}

type copyFromSynaplanRequest struct {
	FileID   int    `json:"fileId"`
	FolderID string `json:"folderId"`
	Name     string `json:"name"`
}

type copyFromSynaplanResponse struct {
	Path string `json:"path"`
}

// CopyFromSynaplan downloads one of the person's Synaplan files and
// writes it into the folder they chose. The file stays in Synaplan.
func (h *Handler) CopyFromSynaplan(w http.ResponseWriter, r *http.Request) {
	var req copyFromSynaplanRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	name := path.Base(strings.TrimSpace(req.Name))
	switch {
	case req.FileID <= 0:
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "fileId is required"})
		return
	case strings.TrimSpace(req.FolderID) == "":
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "folderId is required"})
		return
	case name == "" || name == "." || name == ".." || name == "/":
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "name is required"})
		return
	}
	if h.refuseSharedKey(w) {
		return
	}
	if h.cs3 == nil {
		log.Printf("from synaplan: storage is not configured")
		writeJSON(w, http.StatusBadGateway, copyFailedFailure())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), copyDownloadTimeout)
	defer cancel()
	resp, reqErr := h.synaplanAPI.GetApiFilesDownload(ctx, req.FileID, nil)
	content, err := h.readCappedSynaplanResponse(fmt.Sprintf("download %d", req.FileID), resp, reqErr)
	if err != nil {
		writeCopyFetchFailure(w, "from synaplan", err)
		return
	}

	written, err := h.cs3.SaveChild(ctx, req.FolderID, name, content)
	if err != nil {
		log.Printf("from synaplan: save: %v", err)
		writeJSON(w, http.StatusBadGateway, copyFailedFailure())
		return
	}
	writeJSON(w, http.StatusOK, copyFromSynaplanResponse{Path: written})
}
