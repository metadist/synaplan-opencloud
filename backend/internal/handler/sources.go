package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
	"github.com/metadist/synaplan-opencloud/internal/synced"
)

// sourceEventBatch is the most events Synaplan takes in one request.
const sourceEventBatch = 500

// sourceEvent is one entry of Synaplan's sources/events request. The path
// is not sent: Synaplan only reads it for a move, and a long one would
// make it refuse the whole batch.
type sourceEvent struct {
	ID      string `json:"id"`
	Event   string `json:"event"`
	Version string `json:"version"`
}

// sourceChange is a kept file whose OpenCloud copy moved on, and the event
// that tells Synaplan.
type sourceChange struct {
	kept  synced.File
	event sourceEvent
}

// keptResult counts what one check of a person's kept files did.
type keptResult struct {
	Checked int `json:"checked"`
	Changed int `json:"changed"`
	Deleted int `json:"deleted"`
	Sent    int `json:"sent"`
	Failed  int `json:"failed"`
}

// KnowledgeSyncNow checks the signed-in person's kept files now, with their
// own session, the way the background pass does while they are away.
func (h *Handler) KnowledgeSyncNow(w http.ResponseWriter, r *http.Request) {
	if h.synced == nil || !h.delegation.Configured() {
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: "Keeping files up to date is not set up here. Ask your administrator.",
			Code:  "keep_unavailable",
		})
		return
	}
	sub, err := h.signedInSubject(r.Context())
	if err != nil {
		log.Printf("knowledge sync: who is signed in: %v", err)
		writeJSON(w, http.StatusBadGateway, publicFailure(err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), passTimeout)
	defer cancel()
	result, err := h.keepFilesCurrent(ctx, h.cs3, sub)
	if err != nil {
		writeWatchFailure(w, "knowledge sync", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// forgetKept stops following a file for the signed-in person, coordinated
// under the file's lease so an in-flight background check cannot re-record it.
func (h *Handler) forgetKept(ctx context.Context, resourceID string) {
	if h.synced == nil || !h.delegation.Configured() {
		return
	}
	sub, err := h.signedInSubject(ctx)
	if err != nil {
		log.Printf("knowledge: who is signed in: %v", err)
		return
	}
	held, release, leaseErr := h.synced.Lease(ctx, sub, resourceID)
	if leaseErr == nil {
		defer release()
		ctx = held
	}
	if err := h.synced.Delete(ctx, sub, resourceID, 0); err != nil {
		log.Printf("knowledge: forgetting a kept file: %v", err)
	}
}

// keepFilesCurrent tells Synaplan which of sub's kept files changed or were
// deleted, and uploads the new bytes of those it asks for. ctx carries the
// credentials: the person's session, or their offline token and machine
// auth while they are away. Only the gateway saying a file is not found
// makes it deleted; a gateway that fails skips the file until the next
// check.
func (h *Handler) keepFilesCurrent(ctx context.Context, files watchFiles, sub string) (keptResult, error) {
	var result keptResult
	kept, err := h.synced.Files(ctx, sub)
	if err != nil {
		return result, fmt.Errorf("listing kept files: %w", err)
	}
	var changes []sourceChange
	for _, file := range kept {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result.Checked++
		change, ok := sourceChangeFor(ctx, files, file)
		if !ok {
			continue
		}
		if change.event.Event == "deleted" {
			result.Deleted++
		} else {
			result.Changed++
		}
		changes = append(changes, change)
	}
	for start := 0; start < len(changes); start += sourceEventBatch {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		sent, failed, err := h.sendSourceEvents(ctx, files, changes[start:min(start+sourceEventBatch, len(changes))])
		result.Sent += sent
		result.Failed += failed
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

// sourceChangeFor compares a kept file with its OpenCloud copy.
func sourceChangeFor(ctx context.Context, files watchFiles, kept synced.File) (sourceChange, bool) {
	meta, err := files.StatResource(ctx, kept.ResourceID)
	if errors.Is(err, cs3reader.ErrNotFound) {
		return sourceChange{kept: kept, event: sourceEvent{ID: kept.ResourceID, Event: "deleted", Version: kept.Etag}}, true
	}
	if err != nil {
		log.Printf("kept file: stat: %v", err)
		return sourceChange{}, false
	}
	if meta.Etag == kept.Etag {
		return sourceChange{}, false
	}
	return sourceChange{kept: kept, event: sourceEvent{ID: kept.ResourceID, Event: "changed", Version: meta.Etag}}, true
}

// sendSourceEvents reports one batch and settles each file under its own
// distributed lease so concurrent replicas or manual checks never upload an
// older version over a newer one. A kept version moves on only after
// Synaplan took the event and, when it asked for new bytes, the upload
// indexed at least one chunk.
func (h *Handler) sendSourceEvents(ctx context.Context, files watchFiles, changes []sourceChange) (sent, failed int, err error) {
	eventCtx, cancelEvents := context.WithTimeout(ctx, watchListTimeout)
	events := make([]sourceEvent, 0, len(changes))
	for _, change := range changes {
		events = append(events, change.event)
	}
	payload, err := withJSON(map[string]any{"events": events})
	if err != nil {
		cancelEvents()
		return 0, 0, err
	}
	resp, err := h.synaplanAPI.CloudSourceEventsWithResponse(eventCtx, payload)
	cancelEvents()
	if err != nil {
		return 0, 0, fmt.Errorf("source events: %w", err)
	}
	if resp.StatusCode() != http.StatusOK {
		return 0, 0, fmt.Errorf("source events: %w", upstream(resp.StatusCode(), resp.Body))
	}
	var answer struct {
		Reupload []struct {
			ID string `json:"id"`
		} `json:"reupload"`
	}
	if err := json.Unmarshal(resp.Body, &answer); err != nil {
		return 0, 0, fmt.Errorf("decoding the source events answer: %w", err)
	}
	wanted := make(map[string]bool, len(answer.Reupload))
	for _, item := range answer.Reupload {
		wanted[item.ID] = true
	}
	for _, change := range changes {
		if ctx.Err() != nil {
			return sent, failed, ctx.Err()
		}
		uploaded, fileErr := h.settleSourceChange(ctx, files, change, wanted[change.kept.ResourceID])
		if fileErr != nil {
			log.Printf("kept file %s: %v", change.kept.ResourceID, fileErr)
			failed++
			continue
		}
		if uploaded {
			sent++
		}
	}
	return sent, failed, nil
}

func (h *Handler) settleSourceChange(ctx context.Context, files watchFiles, change sourceChange, reupload bool) (bool, error) {
	kept := change.kept
	held, release, err := h.synced.Lease(ctx, kept.Subject, kept.ResourceID)
	if err != nil {
		return false, fmt.Errorf("taking lease: %w", err)
	}
	defer release()

	// Re-read the stored entry under the lease: if another replica or manual
	// update already removed the file or moved it to a different revision,
	// do not upload stale bytes over it.
	current, found, err := h.synced.Get(held, kept.Subject, kept.ResourceID)
	if err != nil {
		return false, fmt.Errorf("reading kept entry: %w", err)
	}
	if !found || current.Revision != kept.Revision {
		return false, nil
	}

	if change.event.Event == "deleted" {
		if err := h.synced.Delete(held, current.Subject, current.ResourceID, current.Revision); err != nil {
			return false, fmt.Errorf("forgetting deleted file: %w", err)
		}
		return false, nil
	}

	next := current
	next.Etag = change.event.Version
	uploaded := false
	if reupload {
		uploadCtx, cancelUpload := context.WithTimeout(held, watchUploadTimeout)
		version, upErr := h.reuploadKept(uploadCtx, files, current, change.event.Version)
		cancelUpload()
		if upErr != nil {
			if errors.Is(upErr, errFileChanged) {
				log.Printf("kept file %s: changed since scan; next check sends the new version", current.ResourceID)
				return false, nil
			}
			return false, upErr
		}
		next.Etag = version
		uploaded = true
	}
	if _, err := h.synced.Update(held, next); err != nil {
		log.Printf("kept file: recording the version: %v", err)
	}
	return uploaded, nil
}

// reuploadKept replaces Synaplan's copy of a kept file with the scanned
// version's bytes and verifies that Synaplan indexed at least one chunk.
func (h *Handler) reuploadKept(ctx context.Context, files watchFiles, kept synced.File, expectedEtag string) (string, error) {
	opened, err := files.OpenVersion(ctx, kept.ResourceID, expectedEtag)
	if err != nil {
		if errors.Is(err, cs3reader.ErrChanged) {
			return "", errFileChanged
		}
		return "", fmt.Errorf("open: %w", err)
	}
	defer func() { _ = opened.Body.Close() }()
	if opened.Etag != expectedEtag {
		return "", errFileChanged
	}
	if !isTextMime(opened.MimeType) && !isBinaryDocMime(opened.MimeType) {
		return "", fmt.Errorf("%w: %q", errUnsupportedType, opened.MimeType)
	}
	keep := true
	u, err := h.upload(ctx, opened, kept.GroupKey, synaplanapi.Vectorize, &uploadProvenance{
		SourceID:   kept.ResourceID,
		SourceEtag: opened.Etag,
		Overwrite:  true,
		KeepSynced: &keep,
	})
	if err != nil {
		return "", fmt.Errorf("upload: %w", err)
	}
	if u.ChunksCreated == 0 {
		return "", errNoText
	}
	return opened.Etag, nil
}
