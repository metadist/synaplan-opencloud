package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
	"github.com/metadist/synaplan-opencloud/internal/synced"
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
	// KeepSynced asks for later edits of the file to follow in Synaplan.
	KeepSynced bool `json:"keepSynced"`
}

type knowledgeResponse struct {
	GroupKey            string `json:"groupKey"`
	Vectorized          bool   `json:"vectorized"`
	ChunksCreated       int    `json:"chunksCreated"`
	ExtractedTextLength int    `json:"extractedTextLength"`
	SynaplanFileID      int    `json:"synaplanFileId"`
	// KeptUpToDate says whether later edits follow. It is false when they
	// were asked for but this service cannot read the person's files while
	// they are away.
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

	// Edits only follow when the background pass can read the file for the
	// person; without that, Synaplan would wait for uploads that never come.
	var keeper string
	if req.KeepSynced && h.synced != nil {
		keeper = h.awaySubject(r.Context())
	}

	var uploaded *uploadedFile
	var keptUpToDate bool
	// Hold the file's lock before it is opened. A check that starts while
	// this upload waits would otherwise record a newer version, and this
	// request would then store the bytes it already opened.
	if keeper != "" {
		defer h.lockKept(keeper, req.ResourceID)()
	}
	_, ok := h.runSummaryPipeline(w, r, req.ResourceID, "knowledge", func(ctx context.Context, file *cs3reader.File) (string, error) {
		if !isTextMime(file.MimeType) && !isBinaryDocMime(file.MimeType) {
			return "", fmt.Errorf("%w: %q", errUnsupportedType, file.MimeType)
		}
		u, kept, err := h.uploadKnowledge(ctx, file, req.ResourceID, groupKey, keeper, req.Overwrite)
		if err != nil {
			return "", err
		}
		uploaded = u
		keptUpToDate = kept
		return "", nil
	})
	if !ok {
		return
	}
	if keeper == "" {
		h.forgetKept(r.Context(), req.ResourceID)
	}

	writeJSON(w, http.StatusOK, knowledgeResponse{
		GroupKey:            groupKey,
		Vectorized:          uploaded.Vectorized,
		ChunksCreated:       uploaded.ChunksCreated,
		ExtractedTextLength: uploaded.ExtractedTextLength,
		SynaplanFileID:      uploaded.ID,
		KeptUpToDate:        keptUpToDate,
	})
}

// uploadKnowledge sends the file to Synaplan and, when keeper is set,
// records it so later edits follow. The record is written before
// keep_synced is turned on. A store that refuses the record leaves the
// flag off, so Synaplan does not wait for a file this service cannot
// find again. A record that was only a placeholder is removed when the
// upload does not index the file.
func (h *Handler) uploadKnowledge(
	ctx context.Context,
	file *cs3reader.File,
	resourceID, groupKey, keeper string,
	overwrite bool,
) (*uploadedFile, bool, error) {
	follow := keeper != ""
	var kept synced.File
	placed := false
	// unknown means the store could not be read, so the upload must not
	// change keep_synced: a 0 would clear it for a file already kept.
	unknown := false
	if follow {
		row, found, err := h.keptOne(ctx, keeper, resourceID)
		switch {
		case err != nil:
			log.Printf("knowledge: reading the kept file: %v", err)
			follow = false
			unknown = true
		case !found:
			err = h.synced.Put(ctx, synced.File{Subject: keeper, ResourceID: resourceID, GroupKey: groupKey})
			if err != nil {
				log.Printf("knowledge: keeping the file up to date: %v", err)
				follow = false
				break
			}
			row, found, err = h.keptOne(ctx, keeper, resourceID)
			if err != nil || !found || row.Revision == 0 {
				log.Printf("knowledge: reading the placeholder: %v", err)
				follow = false
				break
			}
			placed = true
			kept = row
		default:
			kept = row
		}
	}

	var keepSynced *bool
	switch {
	case unknown:
		keepSynced = nil
	case !follow:
		off := false
		keepSynced = &off
	default:
		on := true
		keepSynced = &on
	}
	uploaded, err := h.upload(ctx, file, groupKey, synaplanapi.Vectorize, &uploadProvenance{
		SourceID:   resourceID,
		SourceEtag: file.Etag,
		Overwrite:  overwrite,
		KeepSynced: keepSynced,
	})
	if err != nil {
		h.dropUnindexed(ctx, kept, placed)
		return nil, false, fmt.Errorf("synaplan upload: %w", err)
	}
	if uploaded.ChunksCreated == 0 {
		h.dropUnindexed(ctx, kept, placed)
		return nil, false, errNoText
	}
	if !follow || keeper == "" {
		return uploaded, false, nil
	}
	kept.Etag = file.Etag
	kept.GroupKey = groupKey
	if _, err = h.synced.Update(ctx, kept); err != nil {
		// The row written beforehand is still there. A new file's empty
		// etag, or the previous etag, makes the next check send this version.
		log.Printf("knowledge: recording the version: %v", err)
	}
	return uploaded, true, nil
}

// dropUnindexed forgets a placeholder written for a file that was not
// indexed, and only that revision. A newer row another writer confirmed
// is left so synchronization keeps its tracking.
func (h *Handler) dropUnindexed(ctx context.Context, kept synced.File, placed bool) {
	if !placed || h.synced == nil || kept.Revision == 0 {
		return
	}
	if err := h.synced.Delete(ctx, kept.Subject, kept.ResourceID, kept.Revision); err != nil {
		log.Printf("knowledge: forgetting a file that was not indexed: %v", err)
	}
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
