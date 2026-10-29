package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Media asks Synaplan to generate an image or a video.
func (h *Handler) Media(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt string `json:"prompt"`
		Type   string `json:"type"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Describe what you want."})
		return
	}
	if req.Type != "image" && req.Type != "video" {
		req.Type = "image"
	}
	status, body, err := h.synaplanCall(r.Context(), http.MethodPost, "/api/v1/media/generate", map[string]string{
		"prompt": req.Prompt,
		"type":   req.Type,
	})
	if err != nil {
		log.Printf("media: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.", Code: "synaplan_unreachable"})
		return
	}
	if status >= 400 {
		writeUpstream(w, status, body)
		return
	}
	writeRaw(w, http.StatusOK, body)
}

// SaveMedia downloads a generated file and writes it into the person's
// Synaplan folder. The file stays in Synaplan as well.
func (h *Handler) SaveMedia(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	rel, ok := generatedFilePath(req.URL)
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "That file cannot be saved."})
		return
	}
	if h.cs3 == nil {
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.", Code: "synaplan_unreachable"})
		return
	}
	status, body, err := h.synaplanDo(r.Context(), http.MethodGet, rel, nil, callOptions{limit: maxCopyBytes})
	if errors.Is(err, errTooLarge) {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "This file is too large to save here.", Code: "file_too_large"})
		return
	}
	if err != nil || status != http.StatusOK {
		log.Printf("media download: status %d err %v", status, err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan could not finish this. Try again in a minute, or ask your administrator.", Code: "synaplan_error"})
		return
	}
	name := path.Base(strings.TrimSpace(req.Name))
	if name == "" || name == "." || name == ".." {
		name = "synaplan.bin"
	}
	written, err := h.cs3.Save(r.Context(), name, body)
	if err != nil {
		log.Printf("media save: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "The file was not saved. It is still in Synaplan.", Code: "synaplan_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"path":       written.Path,
		"resourceId": written.ID,
		"sentence":   "Saved to " + written.Path + ". The file is still in Synaplan.",
	})
}

// uploadsSegment matches one path segment Synaplan uses under
// /api/v1/files/uploads/: a number, or a stored file name without
// separators. Anything else is not a generated file.
var uploadsSegment = regexp.MustCompile(`^[A-Za-z0-9._-]{1,200}$`)

// generatedFilePath turns the URL Synaplan returned for a generated file
// into a path this service is willing to fetch. The path is rebuilt from
// validated parts; nothing from the request is passed through as is.
func generatedFilePath(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "/api/v1/files/"); i >= 0 {
		raw = raw[i:]
	}
	if cut := strings.IndexAny(raw, "?#"); cut >= 0 {
		raw = raw[:cut]
	}
	parts := strings.Split(strings.TrimPrefix(raw, "/api/v1/files/"), "/")
	switch {
	case len(parts) == 2 && parts[1] == "download":
		id, err := strconv.Atoi(parts[0])
		if err != nil || id <= 0 {
			return "", false
		}
		return "/api/v1/files/" + strconv.Itoa(id) + "/download", true
	case len(parts) == 3 && parts[0] == "uploads":
		owner, err := strconv.Atoi(parts[1])
		if err != nil || owner <= 0 || !uploadsSegment.MatchString(parts[2]) {
			return "", false
		}
		return "/api/v1/files/uploads/" + strconv.Itoa(owner) + "/" + url.PathEscape(parts[2]), true
	default:
		return "", false
	}
}
