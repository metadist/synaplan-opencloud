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
	// KeptBlocked says why later edits do not follow ("machine_auth",
	// "consent", or "store") when KeepSynced was asked for.
	KeptBlocked string `json:"keptBlocked,omitempty"`
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
	var keeper, keptBlocked string
	if req.KeepSynced {
		if h.synced == nil {
			keptBlocked = blockedMachineAuth
		} else {
			keeper, keptBlocked = h.awayAllowance(r.Context())
		}
	}

	// Acquire the per-file lease before opening the CS3 file so a
	// concurrent background pass cannot upload a newer version while this
	// upload holds an older open file handle.
	runCtx := r.Context()
	if keeper != "" && h.synced != nil {
		leaseBase, cancelBase := context.WithTimeout(r.Context(), summaryTimeout)
		defer cancelBase()
		held, release, leaseErr := h.synced.Lease(leaseBase, keeper, req.ResourceID)
		if leaseErr != nil {
			log.Printf("knowledge: taking file lease: %v", leaseErr)
			keeper = ""
			keptBlocked = "store"
		} else {
			defer release()
			runCtx = held
		}
	}

	var uploaded *uploadedFile
	var keptUpToDate bool
	_, ok := h.runSummaryPipeline(w, r.WithContext(runCtx), req.ResourceID, "knowledge", func(ctx context.Context, file *cs3reader.File) (string, error) {
		if !readsAsText(file) && !needsExtraction(file) {
			return "", fmt.Errorf("%w: %q", errUnsupportedType, file.MimeType)
		}
		u, kept, blockedReason, err := h.uploadKnowledge(ctx, file, req.ResourceID, groupKey, keeper, req.Overwrite)
		if err != nil {
			return "", err
		}
		uploaded = u
		keptUpToDate = kept
		if blockedReason != "" {
			keptBlocked = blockedReason
		}
		return "", nil
	})
	if !ok {
		return
	}
	if !req.KeepSynced {
		h.forgetKept(r.Context(), req.ResourceID)
		keptBlocked = ""
	} else if keptUpToDate {
		keptBlocked = ""
	}

	writeJSON(w, http.StatusOK, knowledgeResponse{
		GroupKey:            groupKey,
		Vectorized:          uploaded.Vectorized,
		ChunksCreated:       uploaded.ChunksCreated,
		ExtractedTextLength: uploaded.ExtractedTextLength,
		SynaplanFileID:      uploaded.ID,
		KeptUpToDate:        keptUpToDate,
		KeptBlocked:         keptBlocked,
	})
}

// uploadKnowledge sends the file to Synaplan and, when keeper is set,
// records it under the caller's held lease so later edits follow. A
// placeholder is created conditionally before keep_synced=1 is sent so
// Synaplan never waits for updates that have no tracking row. If upload
// or indexing fails, the placeholder is deleted conditionally at its own
// revision so another replica's newer confirmation is never erased.
func (h *Handler) uploadKnowledge(
	ctx context.Context,
	file *cs3reader.File,
	resourceID, groupKey, keeper string,
	overwrite bool,
) (uploaded *uploadedFile, keptUpToDate bool, blockedReason string, err error) {
	follow := keeper != "" && h.synced != nil
	var tracked synced.File
	placed := false
	// unknown means the store could not be read, so the upload must not
	// send keep_synced=0 and clear tracking on a file already kept.
	unknown := false

	if follow {
		row, found, getErr := h.synced.Get(ctx, keeper, resourceID)
		switch {
		case getErr != nil:
			log.Printf("knowledge: reading kept file: %v", getErr)
			follow = false
			unknown = true
			blockedReason = "store"
		case found:
			tracked = row
		default:
			rev, created, createErr := h.synced.Create(ctx, synced.File{
				Subject:    keeper,
				ResourceID: resourceID,
				GroupKey:   groupKey,
			})
			switch {
			case createErr != nil:
				log.Printf("knowledge: creating kept file placeholder: %v", createErr)
				follow = false
				blockedReason = "store"
			case created:
				placed = true
				tracked = synced.File{
					Subject:    keeper,
					ResourceID: resourceID,
					GroupKey:   groupKey,
					Revision:   rev,
				}
			default:
				row, found, getErr = h.synced.Get(ctx, keeper, resourceID)
				if getErr != nil || !found {
					log.Printf("knowledge: reading concurrent kept file: %v", getErr)
					follow = false
					blockedReason = "store"
				} else {
					tracked = row
				}
			}
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
	u, err := h.upload(ctx, file, groupKey, synaplanapi.Vectorize, &uploadProvenance{
		SourceID:   resourceID,
		SourceEtag: file.Etag,
		Overwrite:  overwrite,
		KeepSynced: keepSynced,
	})
	if err != nil {
		h.dropUnindexed(ctx, tracked, placed)
		return nil, false, "", fmt.Errorf("synaplan upload: %w", err)
	}
	if u.ChunksCreated == 0 {
		h.dropUnindexed(ctx, tracked, placed)
		return nil, false, "", errNoText
	}
	if !follow {
		return u, false, blockedReason, nil
	}
	tracked.Etag = file.Etag
	tracked.GroupKey = groupKey
	if _, updateErr := h.synced.Update(ctx, tracked); updateErr != nil {
		// The placeholder or previous row remains in the store with an
		// empty or older etag, so the next pass reconciles this version.
		log.Printf("knowledge: recording the version: %v", updateErr)
	}
	return u, true, "", nil
}

func (h *Handler) dropUnindexed(ctx context.Context, tracked synced.File, placed bool) {
	if !placed || h.synced == nil || tracked.Revision == 0 {
		return
	}
	cleanupCtx := context.WithoutCancel(ctx)
	if err := h.synced.Delete(cleanupCtx, tracked.Subject, tracked.ResourceID, tracked.Revision); err != nil {
		log.Printf("knowledge: forgetting unindexed placeholder: %v", err)
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
