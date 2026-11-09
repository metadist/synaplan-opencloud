package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
)

// ListSynaplanFiles is the person's files in Synaplan, for From Synaplan.
func (h *Handler) ListSynaplanFiles(w http.ResponseWriter, r *http.Request) {
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	status, body, err := h.synaplanCall(r.Context(), http.MethodGet, "/api/v1/files?page="+strconv.Itoa(page)+"&limit=50", nil)
	if err != nil {
		log.Printf("list files: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.", Code: "synaplan_unreachable"})
		return
	}
	writeRaw(w, status, body)
}

// CopyFromSynaplan downloads one of the person's Synaplan files into the
// folder they have open, or into Synaplan/<Kind> when no folder is named.
func (h *Handler) CopyFromSynaplan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FileID   int    `json:"fileId"`
		FolderID string `json:"folderId"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	if req.FileID <= 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Choose a file."})
		return
	}
	if h.cs3 == nil {
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.", Code: "synaplan_unreachable"})
		return
	}
	status, body, err := h.synaplanDo(r.Context(), http.MethodGet, "/api/v1/files/"+strconv.Itoa(req.FileID)+"/download", nil, callOptions{limit: maxCopyBytes})
	if errors.Is(err, errTooLarge) {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "This file is too large to copy here.", Code: "file_too_large"})
		return
	}
	if err != nil || status != http.StatusOK {
		log.Printf("from synaplan download: status %d err %v", status, err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan could not finish this. Try again in a minute, or ask your administrator.", Code: "synaplan_error"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "file"
	}
	var written cs3reader.Written
	if req.FolderID != "" {
		written, err = h.cs3.SaveChild(r.Context(), req.FolderID, name, body)
	} else {
		written, err = h.cs3.Save(r.Context(), name, body)
	}
	if err != nil {
		log.Printf("from synaplan save: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "The file was not copied. It is still in Synaplan.", Code: "synaplan_error"})
		return
	}
	// The written file, not its folder: a watch on the folder keeps
	// seeing other new files, only this one is left out.
	h.store().RememberOwnWrite(currentUserID(r.Context()), written.ID, time.Now().Unix())
	writeJSON(w, http.StatusOK, map[string]any{
		"path":       written.Path,
		"resourceId": written.ID,
		"sentence":   "Copied " + name + " into your files. The file is still in Synaplan.",
	})
}
