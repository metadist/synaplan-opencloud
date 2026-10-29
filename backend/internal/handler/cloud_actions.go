package handler

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"

	"github.com/metadist/synaplan-opencloud/internal/state"
)

func currentUserID(ctx context.Context) string {
	user, ok := revactx.ContextGetUser(ctx)
	if !ok || user.GetId() == nil {
		return ""
	}
	return user.GetId().GetOpaqueId()
}

func (h *Handler) hasInstance() bool {
	return h != nil && h.instanceID != "" && h.instanceSecret != ""
}

// CloudStatus is the bird state for the files in the folder the person
// is looking at. A Synaplan without cloud files answers enabled=false.
func (h *Handler) CloudStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Items []struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	if len(req.Items) == 0 || len(req.Items) > 500 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Send between 1 and 500 files."})
		return
	}
	items := make([]map[string]string, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, map[string]string{"id": item.ID, "version": item.Version})
	}
	status, body, err := h.synaplanCall(r.Context(), http.MethodPost, "/api/v1/cloud/status", map[string]any{"items": items})
	if err != nil {
		log.Printf("cloud status: %v", err)
		writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "unreachable": true, "items": []any{}})
		return
	}
	if status == http.StatusNotFound {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "items": []any{}})
		return
	}
	if status != http.StatusOK {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "unreachable": true, "items": []any{}})
		return
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "unreachable": true, "items": []any{}})
		return
	}
	if _, ok := parsed["enabled"]; !ok {
		parsed["enabled"] = true
	}
	writeJSON(w, http.StatusOK, parsed)
}

// CloudDetail is the sidebar for one Synaplan file.
func (h *Handler) CloudDetail(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("fileId"))
	if id == "" || strings.Contains(id, "/") {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "fileId is required"})
		return
	}
	status, body, err := h.synaplanCall(r.Context(), http.MethodGet, "/api/v1/cloud/files/"+id, nil)
	if err != nil {
		log.Printf("cloud detail: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.",
			Code:  "synaplan_unreachable",
		})
		return
	}
	if status == http.StatusNotFound {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "This file is not searchable.", Code: "file_not_found"})
		return
	}
	writeRaw(w, status, body)
}

// WatchOptions lists the tasks and knowledge folders the watch dialog can offer.
func (h *Handler) WatchOptions(w http.ResponseWriter, r *http.Request) {
	status, body, err := h.synaplanCall(r.Context(), http.MethodGet, "/api/v1/cloud/watches/options", nil)
	if err != nil || status != http.StatusOK {
		log.Printf("watch options: status %d err %v", status, err)
		writeJSON(w, http.StatusOK, map[string]any{
			"tasks": []any{}, "languages": []any{}, "shortcuts": []any{}, "folders": []any{},
			"unavailable": true,
		})
		return
	}
	writeRaw(w, http.StatusOK, body)
}

type watchStartRequest struct {
	TargetKind string `json:"targetKind"`
	TargetID   string `json:"targetId"`
	Preset     string `json:"preset"`
	GroupKey   string `json:"groupKey"`
	TaskID     int    `json:"taskId"`
}

// WatchStart starts a watch on one folder or one file.
func (h *Handler) WatchStart(w http.ResponseWriter, r *http.Request) {
	var req watchStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	if req.TargetKind != "folder" && req.TargetKind != "file" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Choose a folder or a file."})
		return
	}
	if req.Preset != "keep_searchable" && req.Preset != "task" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Choose what should happen."})
		return
	}
	if req.Preset == "task" && req.TaskID <= 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Choose one of your tasks."})
		return
	}
	if req.TargetID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Choose a folder or a file."})
		return
	}
	if h.cs3 == nil {
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.", Code: "synaplan_unreachable"})
		return
	}
	meta, err := h.cs3.StatAny(r.Context(), req.TargetID)
	if err != nil {
		log.Printf("watch stat: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "This file is no longer there. Reload the folder and try again.", Code: "file_not_found"})
		return
	}
	body := map[string]any{
		"target_kind": req.TargetKind,
		"target_id":   req.TargetID,
		"target_path": meta.Path,
		"preset":      req.Preset,
	}
	if req.Preset == "task" {
		body["task_id"] = req.TaskID
	}
	if req.Preset == "keep_searchable" && strings.TrimSpace(req.GroupKey) != "" {
		body["group_key"] = strings.TrimSpace(req.GroupKey)
	}
	status, raw, err := h.synaplanCall(r.Context(), http.MethodPost, "/api/v1/cloud/watches", body)
	if err != nil {
		log.Printf("watch start: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.", Code: "synaplan_unreachable"})
		return
	}
	if status >= 400 {
		writeUpstream(w, status, raw)
		return
	}
	var created struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(raw, &created)
	uid := currentUserID(r.Context())
	if created.ID > 0 {
		_ = h.store().RememberWatch(state.Watch{
			ID: created.ID, TargetKind: req.TargetKind, TargetID: req.TargetID, UserID: uid,
		})
	}
	h.ensureBackgroundKey(r.Context(), uid)
	writeRaw(w, http.StatusOK, raw)
}

// ListSynaplanFiles is the person's files in Synaplan, for From Synaplan.
func (h *Handler) ListSynaplanFiles(w http.ResponseWriter, r *http.Request) {
	page := r.URL.Query().Get("page")
	if page == "" {
		page = "1"
	}
	status, body, err := h.synaplanCall(r.Context(), http.MethodGet, "/api/v1/files?page="+page+"&limit=50", nil)
	if err != nil {
		log.Printf("list files: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.", Code: "synaplan_unreachable"})
		return
	}
	writeRaw(w, status, body)
}

// CopyFromSynaplan downloads one of the person's Synaplan files into the
// folder they have open, or into Synaplan/<Kind> when no folder is named.
func (h *Handler) CopyFromSynaplan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FileID   int    `json:"fileId"`
		FolderID string `json:"folderId"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	if req.FileID <= 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Choose a file."})
		return
	}
	if h.cs3 == nil {
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.", Code: "synaplan_unreachable"})
		return
	}
	status, body, err := h.synaplanDo(r.Context(), http.MethodGet, "/api/v1/files/"+strconv.Itoa(req.FileID)+"/download", nil, callOptions{limit: 100 << 20})
	if err != nil || status != http.StatusOK {
		log.Printf("from synaplan download: status %d err %v", status, err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan could not finish this. Try again in a minute, or ask your administrator.", Code: "synaplan_error"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "file"
	}
	var saved string
	if req.FolderID != "" {
		saved, err = h.cs3.SaveChild(r.Context(), req.FolderID, name, body)
	} else {
		saved, err = h.cs3.Save(r.Context(), name, body)
	}
	if err != nil {
		log.Printf("from synaplan save: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "The file was not copied. It is still in Synaplan.", Code: "synaplan_error"})
		return
	}
	h.store().RememberOwnWrite(currentUserID(r.Context()), req.FolderID, time.Now().Unix())
	writeJSON(w, http.StatusOK, map[string]any{
		"path":     saved,
		"sentence": "Copied " + name + " into your files. The file is still in Synaplan.",
	})
}

// Preferences is the person's read setting and the folder copies land in.
func (h *Handler) Preferences(w http.ResponseWriter, r *http.Request) {
	user := h.store().User(currentUserID(r.Context()))
	writeJSON(w, http.StatusOK, map[string]any{
		"readScope":  user.ReadScope,
		"copyFolder": user.CopyFolder,
		"bound":      h.bound(r.Context()),
	})
}

// SavePreferences stores the read setting and tells Synaplan.
func (h *Handler) SavePreferences(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode       string         `json:"mode"`
		Folders    []state.Folder `json:"folders"`
		CopyFolder string         `json:"copyFolder"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	if req.Mode != "watched" && req.Mode != "allowed" && req.Mode != "all" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Choose what Synaplan may read."})
		return
	}
	if strings.Contains(req.CopyFolder, "..") {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "That folder cannot be used."})
		return
	}
	uid := currentUserID(r.Context())
	user := h.store().User(uid)
	user.ReadScope = state.ReadScope{Mode: req.Mode, Folders: req.Folders}
	user.CopyFolder = strings.Trim(req.CopyFolder, "/")
	if err := h.store().PutUser(uid, user); err != nil {
		log.Printf("save preferences: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan could not finish this. Try again in a minute, or ask your administrator.", Code: "synaplan_error"})
		return
	}
	if h.hasInstance() {
		_, _, err := h.synaplanDo(r.Context(), http.MethodPut, "/api/v1/platform-links/read-scope", map[string]any{
			"external_id": uid,
			"mode":        req.Mode,
			"folders":     req.Folders,
		}, callOptions{extra: h.instanceHeaders()})
		if err != nil {
			log.Printf("report read scope: %v", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true})
}

// Connection says whether this OpenCloud login is bound to Synaplan.
func (h *Handler) Connection(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":       "organisation",
		"configured": h.hasInstance(),
		"bound":      h.bound(r.Context()),
		"serverUrl":  h.synaplanURL,
		"publicUrl":  h.settings.PublicURL,
	})
}

// Bind connects this OpenCloud login to the person's Synaplan account.
func (h *Handler) Bind(w http.ResponseWriter, r *http.Request) {
	if !h.hasInstance() {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "Ask your administrator to connect this OpenCloud to Synaplan.", Code: "not_connected"})
		return
	}
	uid := currentUserID(r.Context())
	status, body, err := h.synaplanDo(r.Context(), http.MethodPost, "/api/v1/platform-links/sso/bind", map[string]string{
		"external_id": uid,
	}, callOptions{extra: h.instanceHeaders()})
	if err != nil {
		log.Printf("sso bind: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.", Code: "synaplan_unreachable"})
		return
	}
	if status >= 400 {
		writeUpstream(w, status, body)
		return
	}
	h.ensureBackgroundKey(r.Context(), uid)
	writeJSON(w, http.StatusOK, map[string]any{"bound": true})
}

func (h *Handler) bound(ctx context.Context) bool {
	if !h.hasInstance() {
		return false
	}
	status, body, err := h.synaplanDo(ctx, http.MethodGet, "/api/v1/platform-links/sso/status?external_id="+currentUserID(ctx), nil, callOptions{extra: h.instanceHeaders()})
	if err != nil || status != http.StatusOK {
		return false
	}
	var parsed struct {
		Bound bool `json:"bound"`
	}
	_ = json.Unmarshal(body, &parsed)
	return parsed.Bound
}

func (h *Handler) ensureBackgroundKey(ctx context.Context, uid string) {
	if uid == "" || !h.hasInstance() {
		return
	}
	if h.store().User(uid).BackgroundKey != "" {
		return
	}
	status, body, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/sso/background-key", map[string]string{
		"external_id": uid,
	}, callOptions{extra: h.instanceHeaders()})
	if err != nil || status != http.StatusOK {
		log.Printf("background key: status %d err %v", status, err)
		return
	}
	var parsed struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Key == "" {
		return
	}
	user := h.store().User(uid)
	user.BackgroundKey = parsed.Key
	if err := h.store().PutUser(uid, user); err != nil {
		log.Printf("store background key: %v", err)
	}
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	if len(body) == 0 {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeUpstream(w http.ResponseWriter, status int, body []byte) {
	var parsed struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	_ = json.Unmarshal(body, &parsed)
	sentence := parsed.Message
	if sentence == "" {
		sentence = parsed.Error
	}
	if sentence == "" || len(sentence) > 300 || strings.Contains(sentence, "Exception") {
		sentence = "Synaplan could not finish this. Try again in a minute, or ask your administrator."
	}
	code := parsed.Code
	if code == "" {
		code = "synaplan_error"
	}
	out := http.StatusBadGateway
	if status >= 400 && status < 500 && status != http.StatusUnauthorized {
		out = status
	}
	writeJSON(w, out, errorResponse{Error: sentence, Code: code})
}
