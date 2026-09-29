package handler

import (
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
	Filename string `json:"filename"`
	Text     string `json:"text"`
}

type saveFileResponse struct {
	Path string `json:"path"`
}

// SaveToSpace writes a text result (a summary or a translation) into
// the signed-in user's personal space, under Synaplan/Documents when
// the name ends in a document extension. The path in the response is
// where the file landed, including a " (2)" suffix when the name was
// already taken.
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

	saved, err := h.cs3.Save(r.Context(), name, []byte(text))
	if err != nil {
		log.Printf("save to space: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not save file: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, saveFileResponse{Path: saved})
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
