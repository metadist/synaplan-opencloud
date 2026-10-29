package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/state"
)

type indexWatch struct {
	ID         int    `json:"id"`
	TargetKind string `json:"target_kind"`
	TargetID   string `json:"target_id"`
	Owner      string `json:"owner_external_id"`
	State      string `json:"state"`
}

// sourceEventBatch is Synaplan's limit per sources/events request.
const sourceEventBatch = 500

// RunReconciler keeps watches and searchable files moving while nobody
// has the folder open. OpenCloud does not deliver file events to an
// extension, so this pass is the way changes are noticed. Each pass has
// a deadline; a Synaplan or a gateway that hangs costs one pass, not all
// of them. The pass waits until the instance is registered.
func (h *Handler) RunReconciler(ctx context.Context) {
	if h == nil {
		return
	}
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		if h.hasInstance() && h.settings.MachineAuthKey != "" {
			passCtx, cancel := context.WithTimeout(ctx, passTimeout)
			h.reconcileOnce(passCtx, "")
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// reconcileOnce runs one pass. An empty onlyUser covers everyone.
func (h *Handler) reconcileOnce(ctx context.Context, onlyUser string) {
	h.syncSourceEvents(ctx, onlyUser)
	h.syncWatches(ctx, onlyUser)
}

// WatchReconcileNow runs one pass for the signed-in person only.
func (h *Handler) WatchReconcileNow(w http.ResponseWriter, r *http.Request) {
	if !h.hasInstance() {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "Ask your administrator to connect this OpenCloud to Synaplan.", Code: "not_connected"})
		return
	}
	uid := currentUserID(r.Context())
	if uid == "" {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "Sign in first."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), passTimeout)
	defer cancel()
	h.reconcileOnce(ctx, uid)
	writeJSON(w, http.StatusOK, map[string]string{"sentence": "Synaplan checked your watched files."})
}

// loadWatchIndex fetches the active watches on this instance. A 200
// answer is authoritative, an empty one too: a watch the person stopped
// in Synaplan is not in it and must not be revived from local memory.
// The local list is used only when Synaplan did not answer.
func (h *Handler) loadWatchIndex(ctx context.Context) []indexWatch {
	callCtx, cancel := bounded(ctx)
	defer cancel()
	status, body, err := h.synaplanDo(callCtx, http.MethodGet, "/api/v1/platform-links/watches?limit=500", nil, callOptions{
		skipUser: true,
		extra:    h.instanceHeaders(),
	})
	h.indexMu.Lock()
	defer h.indexMu.Unlock()
	if err != nil || status != http.StatusOK {
		log.Printf("watch index: status %d err %v", status, err)
		if time.Since(h.indexAt) < time.Hour && h.watchIndex != nil {
			return h.watchIndex
		}
		fallback := make([]indexWatch, 0)
		for _, local := range h.store().Watches() {
			fallback = append(fallback, indexWatch{ID: local.ID, TargetKind: local.TargetKind, TargetID: local.TargetID, Owner: local.UserID, State: "active"})
		}
		return fallback
	}
	var page struct {
		Items []indexWatch `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return h.watchIndex
	}
	if page.Items == nil {
		page.Items = []indexWatch{}
	}
	h.watchIndex = page.Items
	h.indexAt = time.Now()
	known := map[int]bool{}
	for _, item := range page.Items {
		known[item.ID] = true
	}
	for _, local := range h.store().Watches() {
		if !known[local.ID] {
			_ = h.store().ForgetWatch(local.ID)
		}
	}
	return page.Items
}

// activeWatchesFor returns the index rows that belong to one person.
func (h *Handler) activeWatchesFor(ctx context.Context, uid string) []indexWatch {
	var out []indexWatch
	for _, watch := range h.loadWatchIndex(ctx) {
		if watch.Owner == uid && (watch.State == "" || watch.State == "active") {
			out = append(out, watch)
		}
	}
	return out
}

func (h *Handler) syncWatches(ctx context.Context, onlyUser string) {
	for _, watch := range h.loadWatchIndex(ctx) {
		if watch.State != "" && watch.State != "active" {
			continue
		}
		if onlyUser != "" && watch.Owner != onlyUser {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		h.reconcileWatch(ctx, watch)
	}
}

func (h *Handler) reconcileWatch(ctx context.Context, watch indexWatch) {
	if watch.Owner == "" || watch.TargetID == "" || h.cs3 == nil {
		return
	}
	key := h.store().User(watch.Owner).BackgroundKey
	if key == "" {
		return
	}
	ctx, cancel := bounded(ctx)
	defer cancel()
	userCtx, err := h.cs3.Impersonate(ctx, watch.Owner)
	if err != nil {
		log.Printf("watch %d impersonate: %v", watch.ID, err)
		return
	}
	now := time.Now().Unix()
	var rows []cs3reader.Child
	if watch.TargetKind == "file" {
		meta, err := h.cs3.StatAny(userCtx, watch.TargetID)
		if err != nil {
			return
		}
		rows = []cs3reader.Child{{ID: watch.TargetID, Name: meta.Name, Etag: meta.Etag, MimeType: meta.MimeType, Size: meta.Size}}
	} else {
		children, err := h.cs3.List(userCtx, watch.TargetID)
		if err != nil {
			log.Printf("watch %d list: %v", watch.ID, err)
			return
		}
		for _, child := range children {
			if !child.IsFolder {
				rows = append(rows, child)
			}
		}
	}
	payload := make([]map[string]string, 0, len(rows))
	byID := map[string]cs3reader.Child{}
	for _, row := range rows {
		// A file this service wrote a moment ago is not new work for the
		// watch. Only that file is left out, not the folder.
		if !h.settings.AllowSelfWrite && h.store().IsOwnWrite(watch.Owner, row.ID, now) {
			continue
		}
		byID[row.ID] = row
		payload = append(payload, map[string]string{"id": row.ID, "version": row.Etag})
	}
	if len(payload) == 0 {
		return
	}
	status, body, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/cloud/watches/"+strconv.Itoa(watch.ID)+"/reconcile", map[string]any{"items": payload}, callOptions{apiKey: key})
	if err != nil || status != http.StatusOK {
		log.Printf("watch %d reconcile: status %d err %v", watch.ID, status, err)
		return
	}
	var decision struct {
		Process []string `json:"process"`
	}
	_ = json.Unmarshal(body, &decision)
	for _, id := range decision.Process {
		child, ok := byID[id]
		if !ok {
			continue
		}
		if err := h.uploadWatchFile(userCtx, watch.ID, child, key); err != nil {
			log.Printf("watch %d upload %s: %v", watch.ID, child.Name, err)
		}
	}
}

func (h *Handler) uploadWatchFile(ctx context.Context, watchID int, child cs3reader.Child, apiKey string) error {
	file, err := h.cs3.Open(ctx, child.ID)
	if err != nil {
		return err
	}
	defer func() { _ = file.Body.Close() }()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// Synaplan decides readability by the name's extension and falls back
	// to the opaque id when no name is sent, which never has one.
	_ = mw.WriteField("id", child.ID)
	_ = mw.WriteField("version", child.Etag)
	_ = mw.WriteField("name", child.Name)
	_ = mw.WriteField("path", child.Path)
	part, err := filePart(mw, child.Name, child.MimeType)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file.Body); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	status, _, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/cloud/watches/"+strconv.Itoa(watchID)+"/files", nil, callOptions{
		apiKey:      apiKey,
		raw:         bytes.NewReader(buf.Bytes()),
		contentType: mw.FormDataContentType(),
		limit:       1 << 20,
	})
	if err != nil {
		return err
	}
	if status >= 400 {
		return fmt.Errorf("status %d", status)
	}
	return nil
}

// filePart opens the file part of a multipart upload with the file's own
// content type, as the Nextcloud app does. Synaplan reads the type from
// the part; a bare CreateFormFile would send application/octet-stream.
func filePart(mw *multipart.Writer, name, mimeType string) (io.Writer, error) {
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, strings.ReplaceAll(name, `"`, `\"`)))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	header.Set("Content-Type", mimeType)
	return mw.CreatePart(header)
}

// sourceChange is one searchable file whose OpenCloud copy moved on.
type sourceChange struct {
	file  state.Synced
	event map[string]string
	// next is the record to store once Synaplan has the change and,
	// when it asked for one, the new upload succeeded.
	next state.Synced
}

// syncSourceEvents tells Synaplan which searchable files changed or
// were deleted. Only an authoritative NOT_FOUND is a deletion; a gateway
// that is down is not. Events go in batches of 500, and a file's stored
// version moves on only after Synaplan took the event and any upload it
// asked for succeeded, so a failed upload is retried on the next pass.
func (h *Handler) syncSourceEvents(ctx context.Context, onlyUser string) {
	files := h.store().Synced()
	if len(files) == 0 || h.cs3 == nil {
		return
	}
	var changes []sourceChange
	for _, file := range files {
		if onlyUser != "" && file.UserID != onlyUser {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		change, ok := h.sourceChangeFor(ctx, file)
		if ok {
			changes = append(changes, change)
		}
	}
	for start := 0; start < len(changes); start += sourceEventBatch {
		end := start + sourceEventBatch
		if end > len(changes) {
			end = len(changes)
		}
		h.sendSourceEvents(ctx, changes[start:end])
	}
}

func (h *Handler) sourceChangeFor(ctx context.Context, file state.Synced) (sourceChange, bool) {
	callCtx, cancel := bounded(ctx)
	defer cancel()
	userCtx, err := h.cs3.Impersonate(callCtx, file.UserID)
	if err != nil {
		return sourceChange{}, false
	}
	meta, err := h.cs3.StatAny(userCtx, file.ResourceID)
	if errors.Is(err, cs3reader.ErrNotFound) {
		return sourceChange{
			file:  file,
			event: map[string]string{"id": file.ResourceID, "event": "deleted", "version": file.Etag, "path": file.Path},
			next:  state.Synced{},
		}, true
	}
	if err != nil {
		log.Printf("source check %s: %v", file.ResourceID, err)
		return sourceChange{}, false
	}
	if meta.Etag == file.Etag {
		return sourceChange{}, false
	}
	next := file
	next.Etag = meta.Etag
	next.Path = meta.Path
	return sourceChange{
		file:  file,
		event: map[string]string{"id": file.ResourceID, "event": "changed", "version": meta.Etag, "path": meta.Path},
		next:  next,
	}, true
}

func (h *Handler) sendSourceEvents(ctx context.Context, changes []sourceChange) {
	events := make([]map[string]string, 0, len(changes))
	for _, change := range changes {
		events = append(events, change.event)
	}
	callCtx, cancel := bounded(ctx)
	defer cancel()
	status, body, err := h.synaplanDo(callCtx, http.MethodPost, "/api/v1/platform-links/sources/events", map[string]any{"events": events}, callOptions{
		skipUser: true,
		extra:    h.instanceHeaders(),
	})
	if err != nil || status != http.StatusOK {
		log.Printf("source events: status %d err %v", status, err)
		return
	}
	var answer struct {
		Reupload []struct {
			ID string `json:"id"`
		} `json:"reupload"`
	}
	_ = json.Unmarshal(body, &answer)
	wantUpload := map[string]bool{}
	for _, item := range answer.Reupload {
		wantUpload[item.ID] = true
	}
	for _, change := range changes {
		switch {
		case change.event["event"] == "deleted":
			_ = h.store().ForgetSynced(change.file.UserID, change.file.ResourceID)
		case wantUpload[change.file.ResourceID]:
			if h.reuploadSynced(ctx, change.next) {
				_ = h.store().RememberSynced(change.next)
			}
		default:
			_ = h.store().RememberSynced(change.next)
		}
	}
}

// reuploadSynced sends the new bytes of a searchable file. It reports
// whether the upload succeeded, so the caller commits the version only then.
func (h *Handler) reuploadSynced(ctx context.Context, file state.Synced) bool {
	key := h.store().User(file.UserID).BackgroundKey
	if key == "" || h.cs3 == nil {
		return false
	}
	ctx, cancel := bounded(ctx)
	defer cancel()
	userCtx, err := h.cs3.Impersonate(ctx, file.UserID)
	if err != nil {
		return false
	}
	opened, err := h.cs3.Open(userCtx, file.ResourceID)
	if err != nil {
		return false
	}
	defer func() { _ = opened.Body.Close() }()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("files[]", opened.Name)
	if err != nil {
		return false
	}
	if _, err := io.Copy(part, opened.Body); err != nil {
		return false
	}
	_ = mw.WriteField("group_key", file.GroupKey)
	_ = mw.WriteField("process_level", "vectorize")
	_ = mw.WriteField("source", "opencloud")
	_ = mw.WriteField("source_id", file.ResourceID)
	_ = mw.WriteField("source_etag", file.Etag)
	_ = mw.WriteField("overwrite", "1")
	_ = mw.WriteField("keep_synced", "1")
	if err := mw.Close(); err != nil {
		return false
	}
	status, _, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/files/upload", nil, callOptions{
		apiKey:      key,
		raw:         bytes.NewReader(buf.Bytes()),
		contentType: mw.FormDataContentType(),
		limit:       1 << 20,
	})
	if err != nil || status >= 400 {
		log.Printf("reupload %s: status %d err %v", file.ResourceID, status, err)
		return false
	}
	return true
}
