package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
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
	id, secret := h.instance()
	return id != "" && secret != ""
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
	id, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("fileId")))
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "fileId is required"})
		return
	}
	status, body, err := h.synaplanCall(r.Context(), http.MethodGet, "/api/v1/cloud/files/"+strconv.Itoa(id), nil)
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
	uid := currentUserID(r.Context())
	h.linked(r.Context(), uid)
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
	if created.ID > 0 {
		_ = h.store().RememberWatch(state.Watch{
			ID: created.ID, TargetKind: req.TargetKind, TargetID: req.TargetID, UserID: uid,
		})
	}
	// Without the key that works while the person is away, the watch
	// exists in Synaplan but nothing would ever be uploaded. Say so.
	var answer map[string]any
	if err := json.Unmarshal(raw, &answer); err != nil || answer == nil {
		answer = map[string]any{}
	}
	answer["uploads_enabled"] = h.ensureBackgroundKey(r.Context(), uid)
	writeJSON(w, http.StatusOK, answer)
}

// WatchStop ends every watch the person has on one folder or file. The
// authoritative list is Synaplan's; the local copy is only a cache.
func (h *Handler) WatchStop(w http.ResponseWriter, r *http.Request) {
	targetID := strings.TrimSpace(r.URL.Query().Get("targetId"))
	if targetID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "targetId is required"})
		return
	}
	uid := currentUserID(r.Context())
	ids := map[int]struct{}{}
	for _, watch := range h.activeWatchesFor(r.Context(), uid) {
		if watch.TargetID == targetID {
			ids[watch.ID] = struct{}{}
		}
	}
	for _, watch := range h.store().Watches() {
		if watch.UserID == uid && watch.TargetID == targetID {
			ids[watch.ID] = struct{}{}
		}
	}
	stopped := 0
	for id := range ids {
		status, raw, err := h.synaplanCall(r.Context(), http.MethodDelete, "/api/v1/cloud/watches/"+strconv.Itoa(id), nil)
		if err != nil {
			log.Printf("watch stop %d: %v", id, err)
			writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.", Code: "synaplan_unreachable"})
			return
		}
		if status >= 400 && status != http.StatusNotFound {
			writeUpstream(w, status, raw)
			return
		}
		_ = h.store().ForgetWatch(id)
		stopped++
	}
	writeJSON(w, http.StatusOK, map[string]any{"stopped": stopped})
}

// ListSynaplanFiles is the person's files in Synaplan, for From Synaplan.
func (h *Handler) ListSynaplanFiles(w http.ResponseWriter, r *http.Request) {
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	status, body, err := h.synaplanCall(r.Context(), http.MethodGet, "/api/v1/files?page="+strconv.Itoa(page)+"&limit=50", nil)
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
	status, body, err := h.synaplanDo(r.Context(), http.MethodGet, "/api/v1/files/"+strconv.Itoa(req.FileID)+"/download", nil, callOptions{limit: maxCopyBytes})
	if errors.Is(err, errTooLarge) {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "This file is too large to copy here.", Code: "file_too_large"})
		return
	}
	if err != nil || status != http.StatusOK {
		log.Printf("from synaplan download: status %d err %v", status, err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan could not finish this. Try again in a minute, or ask your administrator.", Code: "synaplan_error"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "file"
	}
	var written cs3reader.Written
	if req.FolderID != "" {
		written, err = h.cs3.SaveChild(r.Context(), req.FolderID, name, body)
	} else {
		written, err = h.cs3.Save(r.Context(), name, body)
	}
	if err != nil {
		log.Printf("from synaplan save: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "The file was not copied. It is still in Synaplan.", Code: "synaplan_error"})
		return
	}
	// The written file, not its folder: a watch on the folder keeps
	// seeing other new files, only this one is left out.
	h.store().RememberOwnWrite(currentUserID(r.Context()), written.ID, time.Now().Unix())
	writeJSON(w, http.StatusOK, map[string]any{
		"path":       written.Path,
		"resourceId": written.ID,
		"sentence":   "Copied " + name + " into your files. The file is still in Synaplan.",
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
	folders, err := h.resolveFolders(r.Context(), req.Folders)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "This folder is no longer there. Reload the folder and try again.", Code: "file_not_found"})
		return
	}
	user := h.store().User(uid)
	user.ReadScope = state.ReadScope{Mode: req.Mode, Folders: folders}
	user.CopyFolder = strings.Trim(req.CopyFolder, "/")
	if err := h.store().PutUser(uid, user); err != nil {
		log.Printf("save preferences: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan could not finish this. Try again in a minute, or ask your administrator.", Code: "synaplan_error"})
		return
	}
	if !h.hasInstance() {
		writeJSON(w, http.StatusOK, map[string]any{"saved": true, "reported": false})
		return
	}
	status, _, err := h.synaplanDo(r.Context(), http.MethodPut, "/api/v1/platform-links/read-scope", map[string]any{
		"external_id": uid,
		"mode":        req.Mode,
		"folders":     folders,
	}, callOptions{skipUser: true, extra: h.instanceHeaders()})
	if err != nil || status >= 400 {
		// The setting is kept here and enforced here. Synaplan does not know
		// it yet, so it may still refuse a read, and the person is told.
		log.Printf("report read scope: status %d err %v", status, err)
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "The setting was saved here, but Synaplan did not receive it. Save again in a minute.",
			Code:  "saved_not_reported",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "reported": true})
}

// resolveFolders replaces whatever path the browser sent with the path
// OpenCloud knows for that id, so a folder named like an allowed one, or
// a display name instead of a path, never widens what Synaplan may read.
func (h *Handler) resolveFolders(ctx context.Context, folders []state.Folder) ([]state.Folder, error) {
	out := make([]state.Folder, 0, len(folders))
	for _, folder := range folders {
		if folder.ID == "" {
			continue
		}
		if h.cs3 == nil {
			return nil, fmt.Errorf("resolve folder %s: storage is not configured", folder.ID)
		}
		meta, err := h.cs3.StatAny(ctx, folder.ID)
		if err != nil {
			return nil, fmt.Errorf("resolve folder %s: %w", folder.ID, err)
		}
		out = append(out, state.Folder{ID: folder.ID, Path: canonicalPath(meta.Path)})
	}
	return out, nil
}

// canonicalPath is the space-relative form both the read scope and the
// pickup compare: no leading "./" or "/", no trailing "/".
func canonicalPath(path string) string {
	return strings.Trim(strings.TrimPrefix(path, "./"), "/")
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
	status, body, err := h.bindLogin(r.Context(), uid)
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

// bindLogin tells Synaplan that this OpenCloud login is the person's
// Synaplan login. The person's own token proves it, so the first cloud
// action (a watch, keep up to date) may do it without a separate click.
func (h *Handler) bindLogin(ctx context.Context, uid string) (int, []byte, error) {
	status, body, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/sso/bind", map[string]string{
		"external_id": uid,
	}, callOptions{extra: h.instanceHeaders()})
	if err != nil || status >= 400 {
		return status, body, err
	}
	var bound struct {
		LinkID int `json:"link_id"`
	}
	_ = json.Unmarshal(body, &bound)
	if bound.LinkID > 0 {
		user := h.store().User(uid)
		user.LinkID = bound.LinkID
		if err := h.store().PutUser(uid, user); err != nil {
			log.Printf("store link id: %v", err)
		}
	}
	return status, body, nil
}

// linked makes sure the person's login is bound before a call that needs
// the identity row. A failed bind is left to the call itself to report.
func (h *Handler) linked(ctx context.Context, uid string) {
	if uid == "" || !h.hasInstance() || h.store().User(uid).LinkID > 0 {
		return
	}
	if h.bound(ctx) {
		return
	}
	if _, _, err := h.bindLogin(ctx, uid); err != nil {
		log.Printf("sso bind before cloud action: %v", err)
	}
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

// ensureBackgroundKey makes sure this service holds the key that works
// while the person is away. It reports whether one is held afterwards.
// Synaplan hands the plaintext out once; a later mint without it means
// another instance of this service has it, and that is reported as false.
func (h *Handler) ensureBackgroundKey(ctx context.Context, uid string) bool {
	if uid == "" || !h.hasInstance() {
		return false
	}
	if h.store().User(uid).BackgroundKey != "" {
		return true
	}
	h.linked(ctx, uid)
	status, body, err := h.synaplanDo(ctx, http.MethodPost, "/api/v1/platform-links/sso/background-key", map[string]string{
		"external_id": uid,
	}, callOptions{extra: h.instanceHeaders()})
	if err != nil || status != http.StatusOK {
		log.Printf("background key: status %d err %v", status, err)
		return false
	}
	var parsed struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Key == "" {
		return false
	}
	user := h.store().User(uid)
	user.BackgroundKey = parsed.Key
	if err := h.store().PutUser(uid, user); err != nil {
		log.Printf("store background key: %v", err)
		return false
	}
	return true
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
	// Synaplan answers either {code, error} or {error: <code>, message}.
	code := parsed.Code
	if code == "" && parsed.Message != "" && looksLikeCode(parsed.Error) {
		code = parsed.Error
	}
	if code == "" {
		code = "synaplan_error"
	}
	out := http.StatusBadGateway
	if status >= 400 && status < 500 && status != http.StatusUnauthorized {
		out = status
	}
	writeJSON(w, out, errorResponse{Error: sentence, Code: code})
}

// looksLikeCode tells a machine code such as platform_ambiguous from a
// sentence: lower-case letters, digits and underscores only.
func looksLikeCode(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}
