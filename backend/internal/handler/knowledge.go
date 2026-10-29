package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/state"
	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

// knowledgeGroupMaxLen caps how many characters we accept for a
// group key. Long enough for any sensible tag, short enough to keep
// weird payloads out.
const knowledgeGroupMaxLen = 64

type knowledgeRequest struct {
	ResourceID string `json:"resourceId"`
	GroupKey   string `json:"groupKey"`
	// Overwrite replaces the knowledge copy of this same OpenCloud
	// file instead of adding a second one.
	Overwrite bool `json:"overwrite"`
	// KeepSynced asks Synaplan to follow later edits of this file.
	KeepSynced bool `json:"keepSynced"`
}

type knowledgeResponse struct {
	GroupKey            string `json:"groupKey"`
	Vectorized          bool   `json:"vectorized"`
	ChunksCreated       int    `json:"chunksCreated"`
	ExtractedTextLength int    `json:"extractedTextLength"`
	SynaplanFileID      int    `json:"synaplanFileId"`
	// KeptUpToDate says whether later edits will follow. False when it was
	// asked for but this login holds no key that works while they are away.
	KeptUpToDate bool `json:"keptUpToDate"`
}

// AddToKnowledge reads a file from OpenCloud storage via CS3 and
// uploads it to Synaplan with process_level=vectorize under the
// user-supplied group key, making it available for RAG-powered
// chat and search. Unlike translate/summarize there's no deferred
// cleanup — the file is meant to stay in the knowledge base.
func (h *Handler) AddToKnowledge(w http.ResponseWriter, r *http.Request) {
	var req knowledgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	if req.ResourceID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "resourceId is required"})
		return
	}
	groupKey := strings.TrimSpace(req.GroupKey)
	if groupKey == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "groupKey is required"})
		return
	}
	if len(groupKey) > knowledgeGroupMaxLen {
		writeJSON(w, http.StatusBadRequest, errorResponse{
			Error: fmt.Sprintf("groupKey must be %d characters or fewer", knowledgeGroupMaxLen),
		})
		return
	}

	// Keeping the copy up to date needs the key that works while the person
	// is away. Without it the flag is not sent: Synaplan would wait for an
	// upload that never comes, and the person would be told it is followed.
	userID := currentUserID(r.Context())
	keepSynced := req.KeepSynced && h.ensureBackgroundKey(r.Context(), userID)

	var uploaded *uploadedFile
	var savedPath, savedEtag string
	_, ok := h.runSummaryPipeline(w, r, req.ResourceID, "knowledge", func(ctx context.Context, file *cs3reader.File) (string, error) {
		if !isTextMime(file.MimeType) && !isBinaryDocMime(file.MimeType) {
			return "", fmt.Errorf("unsupported mime type %q: %w", file.MimeType, errClientInput)
		}
		u, err := h.upload(ctx, file, groupKey, synaplanapi.Vectorize, &uploadProvenance{
			SourceID:   req.ResourceID,
			SourceEtag: file.Etag,
			Overwrite:  req.Overwrite,
			KeepSynced: keepSynced,
		})
		if err != nil {
			return "", fmt.Errorf("synaplan upload: %w", err)
		}
		if u.ChunksCreated == 0 {
			return "", fmt.Errorf("no text could be indexed from this file: %w", errClientInput)
		}
		uploaded = u
		savedPath = file.Path
		savedEtag = file.Etag
		return "", nil
	})
	if !ok {
		return
	}

	if keepSynced {
		_ = h.store().RememberSynced(state.Synced{
			ResourceID: req.ResourceID,
			Etag:       savedEtag,
			Path:       savedPath,
			UserID:     userID,
			GroupKey:   groupKey,
		})
	} else {
		_ = h.store().ForgetSynced(userID, req.ResourceID)
	}

	writeJSON(w, http.StatusOK, knowledgeResponse{
		GroupKey:            groupKey,
		Vectorized:          uploaded.Vectorized,
		ChunksCreated:       uploaded.ChunksCreated,
		ExtractedTextLength: uploaded.ExtractedTextLength,
		SynaplanFileID:      uploaded.ID,
		KeptUpToDate:        keepSynced,
	})
}

// synaplanGroup is the shape of a single entry in Synaplan's
// /api/files/groups response. Only the fields we surface to the
// frontend are declared — Synaplan may return more that we ignore.
type synaplanGroup struct {
	Name       string `json:"name"`
	FileCount  int    `json:"file_count,omitempty"`
	TotalSize  int64  `json:"total_size,omitempty"`
	VectorSize int    `json:"vector_size,omitempty"`
}

type knowledgeGroupsResponse struct {
	Groups []synaplanGroup `json:"groups"`
}

// KnowledgeGroups proxies Synaplan's /api/files/groups so the
// frontend can populate its group picker without duplicating the
// shape or learning Synaplan's URLs directly.
func (h *Handler) KnowledgeGroups(w http.ResponseWriter, r *http.Request) {
	resp, err := h.synaplanAPI.GetApiFilesGroupsWithResponse(r.Context(), nil)
	if err != nil {
		log.Printf("knowledge groups: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not fetch groups: " + err.Error()})
		return
	}
	if resp.StatusCode() != http.StatusOK {
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: fmt.Sprintf("groups status %d: %s", resp.StatusCode(), truncate(string(resp.Body), 300)),
		})
		return
	}

	var parsed struct {
		Success bool            `json:"success"`
		Groups  []synaplanGroup `json:"groups"`
	}
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		log.Printf("knowledge groups: parse: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not parse groups response"})
		return
	}

	writeJSON(w, http.StatusOK, knowledgeGroupsResponse{Groups: parsed.Groups})
}
