package handler

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"

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

// SetMaxCopyBytes sets the copy cap from configuration. Zero keeps
// the default.
func (h *Handler) SetMaxCopyBytes(n int64) {
	if n > 0 {
		h.maxCopyBytes = n
	}
}

func (h *Handler) copyLimit() int64 {
	if h != nil && h.maxCopyBytes > 0 {
		return h.maxCopyBytes
	}
	return defaultMaxCopyBytes
}

// synaplanFilesPerPage is how many of the person's Synaplan files one
// page of "From Synaplan" lists.
const synaplanFilesPerPage = 50

// ListSynaplanFiles is one page of the person's own files in Synaplan,
// newest first, for "From Synaplan".
func (h *Handler) ListSynaplanFiles(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.",
			Code:  "synaplan_unreachable",
		})
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
	if h.cs3 == nil {
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not save file: storage is not configured"})
		return
	}

	res, err := h.synaplanAPI.GetApiFilesDownload(r.Context(), req.FileID, nil)
	if err != nil {
		log.Printf("from synaplan: download %d: %v", req.FileID, err)
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.",
			Code:  "synaplan_unreachable",
		})
		return
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		log.Printf("from synaplan: download %d answered %d", req.FileID, res.StatusCode)
		writeJSON(w, http.StatusBadGateway, publicFailure(nil))
		return
	}
	limit := h.copyLimit()
	content, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		log.Printf("from synaplan: read %d: %v", req.FileID, err)
		writeJSON(w, http.StatusBadGateway, publicFailure(nil))
		return
	}
	if int64(len(content)) > limit {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{
			Error: "This file is too large to copy here.",
			Code:  "file_too_large",
		})
		return
	}

	written, err := h.cs3.SaveChild(r.Context(), req.FolderID, name, content)
	if err != nil {
		log.Printf("from synaplan: save: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "The file was not copied. It is still in Synaplan.",
			Code:  "copy_failed",
		})
		return
	}
	writeJSON(w, http.StatusOK, copyFromSynaplanResponse{Path: written})
}
