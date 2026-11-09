package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/metadist/synaplan-opencloud/internal/state"
)

// Preferences is the person's read setting and the folder copies land in.
func (h *Handler) Preferences(w http.ResponseWriter, r *http.Request) {
	user := h.store().User(currentUserID(r.Context()))
	writeJSON(w, http.StatusOK, map[string]any{
		"readScope":  user.ReadScope,
		"copyFolder": user.CopyFolder,
		"bound":      h.hasInstance(),
	})
}

// SavePreferences stores the read setting and tells Synaplan.
func (h *Handler) SavePreferences(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode       string         `json:"mode"`
		Folders    []state.Folder `json:"folders"`
		CopyFolder string         `json:"copyFolder"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	if req.Mode != "watched" && req.Mode != "allowed" && req.Mode != "all" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Choose what Synaplan may read."})
		return
	}
	if strings.Contains(req.CopyFolder, "..") {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "That folder cannot be used."})
		return
	}
	uid := currentUserID(r.Context())
	folders, err := h.resolveFolders(r.Context(), req.Folders)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "This folder is no longer there. Reload the folder and try again.", Code: "file_not_found"})
		return
	}
	user := h.store().User(uid)
	user.ReadScope = state.ReadScope{Mode: req.Mode, Folders: folders}
	user.CopyFolder = strings.Trim(req.CopyFolder, "/")
	if err := h.store().PutUser(uid, user); err != nil {
		log.Printf("save preferences: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan could not finish this. Try again in a minute, or ask your administrator.", Code: "synaplan_error"})
		return
	}
	if !h.hasInstance() {
		writeJSON(w, http.StatusOK, map[string]any{"saved": true, "reported": false})
		return
	}
	status, _, err := h.synaplanDo(r.Context(), http.MethodPut, "/api/v1/platform-links/read-scope", map[string]any{
		"external_id": uid,
		"mode":        req.Mode,
		"folders":     folders,
	}, callOptions{})
	if err != nil || status >= 400 {
		// The setting is kept here and enforced here. Synaplan does not know
		// it yet, so it may still refuse a read, and the person is told.
		log.Printf("report read scope: status %d err %v", status, err)
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "The setting was saved here, but Synaplan did not receive it. Save again in a minute.",
			Code:  "saved_not_reported",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "reported": true})
}

// resolveFolders replaces whatever path the browser sent with the path
// OpenCloud knows for that id, so a folder named like an allowed one, or
// a display name instead of a path, never widens what Synaplan may read.
func (h *Handler) resolveFolders(ctx context.Context, folders []state.Folder) ([]state.Folder, error) {
	out := make([]state.Folder, 0, len(folders))
	for _, folder := range folders {
		if folder.ID == "" {
			continue
		}
		if h.cs3 == nil {
			return nil, fmt.Errorf("resolve folder %s: storage is not configured", folder.ID)
		}
		meta, err := h.cs3.StatAny(ctx, folder.ID)
		if err != nil {
			return nil, fmt.Errorf("resolve folder %s: %w", folder.ID, err)
		}
		out = append(out, state.Folder{ID: folder.ID, Path: canonicalPath(meta.Path)})
	}
	return out, nil
}
