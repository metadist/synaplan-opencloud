package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
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

func (h *Handler) syncWatches(ctx context.Context) {
	status, body, err := h.synaplanDo(ctx, http.MethodGet, "/api/v1/platform-links/watches?limit=500", nil, callOptions{
		skipUser: true,
		extra:    h.instanceHeaders(),
	})
	if err != nil || (status != http.StatusOK && status != http.StatusNotModified) {
		log.Printf("watch index: status %d err %v", status, err)
		return
	}
	var page struct {
		Items []indexWatch `json:"items"`
	}
	_ = json.Unmarshal(body, &page)
	if len(page.Items) == 0 {
		for _, local := range h.store().Watches() {
			page.Items = append(page.Items, indexWatch{
				ID: local.ID, TargetKind: local.TargetKind, TargetID: local.TargetID, Owner: local.UserID, State: "active",
			})
		}
	}
	for _, watch := range page.Items {
		if watch.State != "" && watch.State != "active" {
			continue
		}
		h.reconcileWatch(ctx, watch)
	}
}

func (h *Handler) reconcileWatch(ctx context.Context, watch indexWatch) {
	if watch.Owner == "" || watch.TargetID == "" || h.cs3 == nil {
		return
	}
	if !h.settings.AllowSelfWrite && h.store().IsOwnWrite(watch.Owner, watch.TargetID, time.Now().Unix()) {
		return
	}
	key := h.store().User(watch.Owner).BackgroundKey
	if key == "" {
		return
	}
	userCtx, err := h.cs3.Impersonate(ctx, watch.Owner)
	if err != nil {
		log.Printf("watch %d impersonate: %v", watch.ID, err)
		return
	}
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
	_ = mw.WriteField("id", child.ID)
	_ = mw.WriteField("version", child.Etag)
	part, err := mw.CreateFormFile("file", child.Name)
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

func (h *Handler) syncSourceEvents(ctx context.Context) {
	files := h.store().Synced()
	if len(files) == 0 || h.cs3 == nil {
		return
	}
	var events []map[string]string
	var changed []state.Synced
	for _, file := range files {
		userCtx, err := h.cs3.Impersonate(ctx, file.UserID)
		if err != nil {
			continue
		}
		meta, err := h.cs3.StatAny(userCtx, file.ResourceID)
		if err != nil {
			events = append(events, map[string]string{"id": file.ResourceID, "event": "deleted", "version": file.Etag, "path": file.Path})
			continue
		}
		if meta.Etag != file.Etag {
			events = append(events, map[string]string{"id": file.ResourceID, "event": "changed", "version": meta.Etag, "path": meta.Path})
			file.Etag = meta.Etag
			file.Path = meta.Path
			changed = append(changed, file)
		}
	}
	if len(events) == 0 {
		return
	}
	status, body, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/sources/events", map[string]any{"events": events}, callOptions{
		skipUser: true,
		extra:    h.instanceHeaders(),
	})
	if err != nil || status != http.StatusOK {
		log.Printf("source events: status %d err %v", status, err)
		return
	}
	for _, file := range changed {
		_ = h.store().RememberSynced(file)
	}
	var answer struct {
		Reupload []struct {
			ID string `json:"id"`
		} `json:"reupload"`
	}
	_ = json.Unmarshal(body, &answer)
	want := map[string]bool{}
	for _, item := range answer.Reupload {
		want[item.ID] = true
	}
	for _, file := range changed {
		if want[file.ResourceID] {
			h.reuploadSynced(ctx, file)
		}
	}
}

func (h *Handler) reuploadSynced(ctx context.Context, file state.Synced) {
	key := h.store().User(file.UserID).BackgroundKey
	if key == "" || h.cs3 == nil {
		return
	}
	userCtx, err := h.cs3.Impersonate(ctx, file.UserID)
	if err != nil {
		return
	}
	opened, err := h.cs3.Open(userCtx, file.ResourceID)
	if err != nil {
		return
	}
	defer func() { _ = opened.Body.Close() }()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("files[]", opened.Name)
	if err != nil {
		return
	}
	if _, err := io.Copy(part, opened.Body); err != nil {
		return
	}
	_ = mw.WriteField("group_key", file.GroupKey)
	_ = mw.WriteField("process_level", "vectorize")
	_ = mw.WriteField("source", "opencloud")
	_ = mw.WriteField("source_id", file.ResourceID)
	_ = mw.WriteField("source_etag", file.Etag)
	_ = mw.WriteField("overwrite", "1")
	_ = mw.WriteField("keep_synced", "1")
	if err := mw.Close(); err != nil {
		return
	}
	status, _, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/files/upload", nil, callOptions{
		apiKey:      key,
		raw:         bytes.NewReader(buf.Bytes()),
		contentType: mw.FormDataContentType(),
		limit:       1 << 20,
	})
	if err != nil || status >= 400 {
		log.Printf("reupload %s: status %d err %v", file.ResourceID, status, err)
	}
}
