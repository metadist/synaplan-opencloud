package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"

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

// forgetKept stops following a file for the signed-in person. It is best
// effort: a file that stays listed costs one stat per pass, and Synaplan
// asks for no upload of a copy it no longer keeps up to date.
func (h *Handler) forgetKept(ctx context.Context, resourceID string) {
	if h.synced == nil || !h.delegation.Configured() {
		return
	}
	sub, err := h.signedInSubject(ctx)
	if err != nil {
		log.Printf("knowledge: who is signed in: %v", err)
		return
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
		return result, err
	}
	slices.SortFunc(kept, func(a, b synced.File) int { return strings.Compare(a.ResourceID, b.ResourceID) })
	for start := 0; start < len(kept); start += sourceEventBatch {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		batch := kept[start:min(start+sourceEventBatch, len(kept))]
		if err := h.syncKeptBatch(ctx, files, sub, batch, &result); err != nil {
			return result, err
		}
	}
	return result, nil
}

// syncKeptBatch compares one batch and settles it while this process holds
// each file's lock, so a second pass cannot upload an older copy over the
// version the first pass just stored.
func (h *Handler) syncKeptBatch(ctx context.Context, files watchFiles, sub string, batch []synced.File, result *keptResult) error {
	unlock := h.lockKeptFiles(batch)
	defer unlock()

	fresh, err := h.synced.Files(ctx, sub)
	if err != nil {
		return err
	}
	byID := make(map[string]synced.File, len(fresh))
	for _, file := range fresh {
		byID[file.ResourceID] = file
	}
	var changes []sourceChange
	for _, file := range batch {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		current, ok := byID[file.ResourceID]
		if !ok {
			continue
		}
		result.Checked++
		change, ok := sourceChangeFor(ctx, files, current)
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
	if len(changes) == 0 {
		return nil
	}
	sent, failed, err := h.sendSourceEvents(ctx, files, changes)
	result.Sent += sent
	result.Failed += failed
	return err
}

// lockKeptFiles holds one mutex per file, in resource id order, until the
// returned function runs. The same process cannot overlap a file with
// itself. A stale commit is still refused by the store revision. Another
// replica has no lease, so it can still upload after a newer version was
// stored.
func (h *Handler) lockKeptFiles(files []synced.File) func() {
	ordered := slices.Clone(files)
	slices.SortFunc(ordered, func(a, b synced.File) int { return strings.Compare(a.ResourceID, b.ResourceID) })
	unlocks := make([]func(), 0, len(ordered))
	for _, file := range ordered {
		unlocks = append(unlocks, h.lockKept(file.Subject, file.ResourceID))
	}
	return func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
}

// lockKept returns the unlock for subject and resourceID.
func (h *Handler) lockKept(subject, resourceID string) func() {
	key := subject + "\x00" + resourceID
	h.keptMu.Lock()
	if h.keptLocks == nil {
		h.keptLocks = map[string]*sync.Mutex{}
	}
	mu := h.keptLocks[key]
	if mu == nil {
		mu = &sync.Mutex{}
		h.keptLocks[key] = mu
	}
	h.keptMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// keptOne reads the file the store currently keeps for subject.
func (h *Handler) keptOne(ctx context.Context, subject, resourceID string) (synced.File, bool, error) {
	if h.synced == nil {
		return synced.File{}, false, nil
	}
	files, err := h.synced.Files(ctx, subject)
	if err != nil {
		return synced.File{}, false, err
	}
	for _, file := range files {
		if file.ResourceID == resourceID {
			return file, true, nil
		}
	}
	return synced.File{}, false, nil
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

// sendSourceEvents reports one batch and settles each file. A kept version
// moves on only after Synaplan took the event and, when it asked for the
// new bytes, the upload succeeded; otherwise the next check reports the
// change again.
func (h *Handler) sendSourceEvents(ctx context.Context, files watchFiles, changes []sourceChange) (sent, failed int, err error) {
	ctx, cancel := context.WithTimeout(ctx, watchTimeout)
	defer cancel()
	events := make([]sourceEvent, 0, len(changes))
	for _, change := range changes {
		events = append(events, change.event)
	}
	payload, err := withJSON(map[string]any{"events": events})
	if err != nil {
		return 0, 0, err
	}
	resp, err := h.synaplanAPI.CloudSourceEventsWithResponse(ctx, payload)
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
		oneSent, oneFailed := h.commitKept(ctx, files, change, wanted[change.kept.ResourceID])
		sent += oneSent
		failed += oneFailed
	}
	return sent, failed, nil
}

// commitKept stores the version the event named when that revision is
// still current. A newer revision already won: the upload is not started,
// so Synaplan is not moved back to the older bytes. A refused commit after
// an upload can still happen when another replica races; the store keeps
// the newer version.
func (h *Handler) commitKept(ctx context.Context, files watchFiles, change sourceChange, upload bool) (sent, failed int) {
	kept := change.kept
	current, ok, err := h.keptOne(ctx, kept.Subject, kept.ResourceID)
	if err != nil {
		log.Printf("kept file: reading it again: %v", err)
		return 0, 1
	}
	if !ok || current.Revision != kept.Revision {
		log.Printf("kept file: a newer version is already recorded")
		return 0, 0
	}
	if change.event.Event == "deleted" {
		if err := h.synced.Delete(ctx, current.Subject, current.ResourceID, current.Revision); err != nil {
			log.Printf("kept file: forgetting: %v", err)
		}
		return 0, 0
	}
	next := current
	next.Etag = change.event.Version
	if upload {
		version, err := h.reuploadKept(ctx, files, current, change.event.Version)
		if errors.Is(err, errFileChanged) {
			log.Printf("kept file: %s changed; the next check sends the new version", current.ResourceID)
			return 0, 0
		}
		if err != nil {
			log.Printf("kept file: upload: %v", err)
			return 0, 1
		}
		next.Etag = version
		sent = 1
	}
	updated, err := h.synced.Update(ctx, next)
	if err != nil {
		log.Printf("kept file: recording the version: %v", err)
		return sent, 0
	}
	if !updated {
		log.Printf("kept file: a newer version is already recorded")
	}
	return sent, 0
}

// reuploadKept replaces Synaplan's copy with the version the event named.
// A file that changed again is left alone, and an upload that indexes
// nothing is an error: either way the stored version stays pending so the
// next check sends the file again.
func (h *Handler) reuploadKept(ctx context.Context, files watchFiles, kept synced.File, version string) (string, error) {
	opened, err := files.OpenVersion(ctx, kept.ResourceID, version)
	if err != nil {
		if errors.Is(err, cs3reader.ErrChanged) {
			return "", errFileChanged
		}
		return "", err
	}
	defer func() { _ = opened.Body.Close() }()
	if opened.Etag != version {
		return "", errFileChanged
	}
	keep := true
	uploaded, err := h.upload(ctx, opened, kept.GroupKey, synaplanapi.Vectorize, &uploadProvenance{
		SourceID:   kept.ResourceID,
		SourceEtag: opened.Etag,
		Overwrite:  true,
		KeepSynced: &keep,
	})
	if err != nil {
		return "", err
	}
	if uploaded.ChunksCreated == 0 {
		return "", errNoText
	}
	return opened.Etag, nil
}
