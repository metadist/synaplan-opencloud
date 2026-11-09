package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/metadist/synaplan-opencloud/internal/state"
)

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
	answer["uploads_enabled"] = h.canActAway(uid)
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
