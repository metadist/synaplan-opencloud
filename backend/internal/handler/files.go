package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"
)

const maxSavedTextBytes = 2 << 20

// maxSaveBodyBytes bounds the JSON body before it is decoded. The
// text field itself is capped separately at maxSavedTextBytes; this
// extra room is only for the filename and JSON framing.
const maxSaveBodyBytes = maxSavedTextBytes + 64<<10

type saveFileRequest struct {
	Filename   string `json:"filename"`
	Text       string `json:"text"`
	ResourceID string `json:"resourceId"`
}

type saveFileResponse struct {
	Path string `json:"path"`
}

// SaveToSpace writes a text result (a summary or a translation).
// When resourceId names the source file, the result is saved in that
// file's folder, next to the source, through SaveChild. Without it,
// Save picks the personal Synaplan folder and SaveChild writes there.
// The path in the response is where the file landed, including a
// " (2)" suffix when the name was already taken.
func (h *Handler) SaveToSpace(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSaveBodyBytes)
	var req saveFileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) || strings.Contains(err.Error(), "request body too large") {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "text is too large to save"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	name, errMsg := cleanSavedName(req.Filename)
	if errMsg != "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: errMsg})
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "text is required"})
		return
	}
	if len(text) > maxSavedTextBytes {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "text is too large to save"})
		return
	}
	if h.cs3 == nil {
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not save file: storage is not configured"})
		return
	}

	saved, err := h.saveText(r.Context(), name, []byte(text), strings.TrimSpace(req.ResourceID))
	if err != nil {
		log.Printf("save to space: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not save file: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, saveFileResponse{Path: saved})
}

// saveText is the one write of a text result. A source file selects
// its own folder; otherwise the personal Synaplan folder is the target.
// Both end in SaveChild. A folder the person cannot write, such as a
// read-only share, falls back to the personal Synaplan folder. The
// path in the response is where the file landed.
func (h *Handler) saveText(ctx context.Context, name string, text []byte, resourceID string) (string, error) {
	if resourceID != "" {
		folderID, err := h.cs3.ParentFolderID(ctx, resourceID)
		if err == nil {
			saved, saveErr := h.cs3.SaveChild(ctx, folderID, name, text)
			if saveErr == nil {
				return saved, nil
			}
			err = saveErr
		}
		log.Printf("save beside source: %v", err)
	}
	return h.cs3.Save(ctx, name, text)
}

func cleanSavedName(raw string) (string, string) {
	name := path.Base(strings.TrimSpace(raw))
	if name == "" || name == "." || name == ".." {
		return "", "filename is required"
	}
	if strings.ContainsAny(name, "/\\") || strings.ContainsRune(name, 0) {
		return "", "filename is invalid"
	}
	if utf8.RuneCountInString(name) > 120 {
		return "", "filename is too long"
	}
	return name, ""
}
