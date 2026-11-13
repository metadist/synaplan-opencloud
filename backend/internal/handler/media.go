package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

const (
	maxImagePromptRunes = 4000
	maxImageBytes       = 50 << 20
	maxMediaBodyBytes   = 64 << 10
	imageTimeout        = 5 * time.Minute
)

var errImageTooLarge = errors.New("generated file is too large")

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

	ctx, cancel := context.WithTimeout(r.Context(), imageTimeout)
	defer cancel()
	resp, err := h.synaplanAPI.PostApiMediaGenerateWithResponse(ctx, synaplanapi.PostApiMediaGenerateJSONRequestBody{
		Prompt: prompt,
		Type:   synaplanapi.PostApiMediaGenerateJSONBodyTypeImage,
	})
	if err != nil {
		log.Printf("media/generate: %v", err)
		writeJSON(w, http.StatusBadGateway, publicFailure(err))
		return
	}
	if resp.StatusCode() == http.StatusUnprocessableEntity {
		writeJSON(w, http.StatusUnprocessableEntity, errorResponse{Error: "This Synaplan has no model that makes images. Ask your administrator.", Code: "image_unavailable"})
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
// space, under Synaplan/Images, through the same write path as a saved
// summary. The image stays in Synaplan as well.
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
	if h.cs3 == nil {
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not save file: storage is not configured"})
		return
	}

	content, err := h.fetchGenerated(r.Context(), ref)
	if errors.Is(err, errImageTooLarge) {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "This image is too large to save here.", Code: "file_too_large"})
		return
	}
	if err != nil {
		log.Printf("media/save: %v", err)
		writeJSON(w, http.StatusBadGateway, publicFailure(err))
		return
	}
	saved, err := h.cs3.Save(r.Context(), name, content)
	if err != nil {
		log.Printf("media/save: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not save file: " + err.Error()})
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
	raw = strings.TrimSpace(raw)
	i := strings.Index(raw, "/api/v1/files/")
	if i < 0 {
		return generatedFile{}, false
	}
	raw = raw[i+len("/api/v1/files/"):]
	if cut := strings.IndexAny(raw, "?#"); cut >= 0 {
		raw = raw[:cut]
	}
	parts := strings.Split(raw, "/")
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

// fetchGenerated reads a generated file from Synaplan, at most
// maxImageBytes of it.
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
	if err != nil {
		return nil, fmt.Errorf("fetching generated file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading generated file: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, wrapUpstreamStatus("fetching generated file", resp.StatusCode, body)
	}
	if len(body) > maxImageBytes {
		return nil, errImageTooLarge
	}
	return body, nil
}

// keepSlashes sends a multi-segment path parameter with its slashes:
// the generated client escapes them to %2F, which Synaplan's
// uploads/{path} route does not match behind every web server.
func keepSlashes(_ context.Context, req *http.Request) error {
	req.URL.RawPath = ""
	return nil
}
