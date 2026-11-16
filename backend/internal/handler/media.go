package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

const (
	maxImagePromptRunes = 4000
	maxMediaBodyBytes   = 64 << 10
)

// imageTimeout bounds one image generation and the copy of its file.
var imageTimeout = 5 * time.Minute

type generateImageRequest struct {
	Prompt string `json:"prompt"`
}

type generateImageResponse struct {
	URL      string `json:"url"`
	MimeType string `json:"mimeType"`
}

type saveImageRequest struct {
	URL  string `json:"url"`
	Name string `json:"name"`
}

// refuseMediaSharedKey closes the image routes when every OpenCloud user
// shares one Synaplan key. With one key SaveImage could fetch any file
// belonging to the key owner's Synaplan account.
func (h *Handler) refuseMediaSharedKey(w http.ResponseWriter) bool {
	if h == nil || !h.sharedKey {
		return false
	}
	writeJSON(w, http.StatusForbidden, errorResponse{
		Error: "Making and saving images needs each person signed in to Synaplan. This installation uses one shared key.",
		Code:  "image_shared_key",
	})
	return true
}

// GenerateImage asks Synaplan for an image from a prompt, with the
// person's default image model. The image stays in Synaplan; the
// response names it so SaveImage can copy it into the person's files.
func (h *Handler) GenerateImage(w http.ResponseWriter, r *http.Request) {
	var req generateImageRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMediaBodyBytes)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "prompt is required"})
		return
	}
	if utf8.RuneCountInString(prompt) > maxImagePromptRunes {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "prompt is too long"})
		return
	}

	if h.refuseMediaSharedKey(w) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), imageTimeout)
	defer cancel()
	resp, err := h.synaplanAPI.PostApiMediaGenerateWithResponse(ctx, synaplanapi.PostApiMediaGenerateJSONRequestBody{
		Prompt: prompt,
		Type:   synaplanapi.PostApiMediaGenerateJSONBodyTypeImage,
	})
	if err != nil {
		log.Printf("media/generate: %v", err)
		writeJSON(w, http.StatusBadGateway, unreachableFailure())
		return
	}
	switch resp.StatusCode() {
	case http.StatusUnprocessableEntity:
		writeJSON(w, http.StatusUnprocessableEntity, errorResponse{
			Error: "This Synaplan has no model that makes images. Ask your administrator.",
			Code:  "image_unavailable",
		})
		return
	case http.StatusTooManyRequests:
		writeJSON(w, http.StatusTooManyRequests, errorResponse{
			Error: "You have made as many images as Synaplan allows right now. Try again later.",
			Code:  "image_rate_limited",
		})
		return
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil || resp.JSON200.File.Url == "" {
		err := wrapUpstreamStatus("media/generate", resp.StatusCode(), resp.Body)
		log.Printf("media/generate: %v", err)
		writeJSON(w, http.StatusBadGateway, publicFailure(err))
		return
	}
	writeJSON(w, http.StatusOK, generateImageResponse{URL: resp.JSON200.File.Url, MimeType: resp.JSON200.File.MimeType})
}

// SaveImage copies an image Synaplan made into the person's personal
// space, always under Synaplan/Images. The image stays in Synaplan as
// well. Fetching it and writing it share one bounded deadline.
func (h *Handler) SaveImage(w http.ResponseWriter, r *http.Request) {
	var req saveImageRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMediaBodyBytes)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	ref, ok := parseGeneratedFile(req.URL)
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "url does not name a file in Synaplan"})
		return
	}
	name, errMsg := cleanSavedName(req.Name)
	if errMsg != "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: errMsg})
		return
	}
	if h.refuseMediaSharedKey(w) {
		return
	}
	if h.cs3 == nil {
		log.Printf("media/save: storage is not configured")
		writeJSON(w, http.StatusBadGateway, copyFailedFailure())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), imageTimeout)
	defer cancel()
	content, err := h.fetchGenerated(ctx, ref)
	if err != nil {
		writeCopyFetchFailure(w, "media/save", err)
		return
	}
	saved, err := h.cs3.SaveInKind(ctx, "Images", name, content)
	if err != nil {
		log.Printf("media/save: %v", err)
		writeJSON(w, http.StatusBadGateway, copyFailedFailure())
		return
	}
	writeJSON(w, http.StatusOK, saveFileResponse{Path: saved})
}

// generatedFile names a file in Synaplan this service is willing to
// fetch: a stored upload by its sharded path, or a file by its id.
type generatedFile struct {
	uploadPath string
	id         int
}

// uploadsSegment matches one segment of a path Synaplan stores files
// under (uploads/04/000/00004/2026/11/media_4_test_1.png).
var uploadsSegment = regexp.MustCompile(`^[A-Za-z0-9._-]{1,200}$`)

// parseGeneratedFile accepts the URL Synaplan returned for a generated
// file, relative or absolute, and keeps only the parts that name the
// file. The host is dropped: the file is always fetched from
// SYNAPLAN_URL with the person's own credential.
func parseGeneratedFile(raw string) (generatedFile, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return generatedFile{}, false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return generatedFile{}, false
	}
	if parsed.Scheme != "" && parsed.Scheme != "http" && parsed.Scheme != "https" {
		return generatedFile{}, false
	}
	// RawPath is non-empty when any path character was percent-encoded
	// (such as %2F); reject encoded paths rather than re-interpreting them.
	if parsed.RawPath != "" && parsed.RawPath != parsed.Path {
		return generatedFile{}, false
	}
	const marker = "/api/v1/files/"
	i := strings.Index(parsed.Path, marker)
	if i < 0 {
		return generatedFile{}, false
	}
	rest := parsed.Path[i+len(marker):]
	parts := strings.Split(rest, "/")
	switch {
	case len(parts) == 2 && parts[1] == "download":
		id, err := strconv.Atoi(parts[0])
		if err != nil || id <= 0 {
			return generatedFile{}, false
		}
		return generatedFile{id: id}, true
	case len(parts) >= 2 && len(parts) <= 8 && parts[0] == "uploads":
		for _, part := range parts[1:] {
			if part == "." || part == ".." || !uploadsSegment.MatchString(part) {
				return generatedFile{}, false
			}
		}
		return generatedFile{uploadPath: strings.Join(parts[1:], "/")}, true
	default:
		return generatedFile{}, false
	}
}

// fetchGenerated reads a generated file from Synaplan, at most the copy
// cap (SYNAPLAN_MAX_COPY_BYTES) of it.
func (h *Handler) fetchGenerated(ctx context.Context, ref generatedFile) ([]byte, error) {
	var (
		resp *http.Response
		err  error
	)
	if ref.id > 0 {
		resp, err = h.synaplanAPI.GetApiFilesDownload(ctx, ref.id, nil)
	} else {
		resp, err = h.synaplanAPI.GetServeStaticUpload(ctx, ref.uploadPath, keepSlashes)
	}
	return h.readCappedSynaplanResponse("fetching generated file", resp, err)
}
