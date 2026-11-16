package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// Watch presets, target kinds, states and upload-block reasons shared across
// the watch routes and background reconciliation.
const (
	presetSearchable = "keep_searchable"
	presetTask       = "task"

	targetFolder = "folder"
	targetFile   = "file"

	watchStateActive    = "active"
	watchStatePaused    = "paused"
	watchStateAttention = "attention"

	blockedMachineAuth = "machine_auth"
	blockedConsent     = "consent"
)

// maxGroupKey is the longest knowledge folder name Synaplan keeps.
const maxGroupKey = 64

// watch is one row of Synaplan's watch list. Synaplan is the only record
// of a person's watches; this service keeps none.
type watch struct {
	ID         int    `json:"id"`
	TargetKind string `json:"target_kind"`
	TargetID   string `json:"target_id"`
	State      string `json:"state"`
}

// upstreamError is a Synaplan answer other than 200 to a watch call.
type upstreamError struct {
	status int
	code   string
	text   string
}

func (e *upstreamError) Error() string {
	return fmt.Sprintf("synaplan answered %d %s: %s", e.status, e.code, truncate(e.text, 300))
}

// upstream reads a Synaplan error body, {"error": code, "message": sentence}.
func upstream(status int, body []byte) *upstreamError {
	var parsed struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &parsed)
	return &upstreamError{status: status, code: parsed.Error, text: parsed.Message}
}

// watchFailure is what a person sees when a watch call fails: one sentence
// and a code the extension translates. Synaplan's own sentences are only
// passed on where they carry a number the person needs (the watch cap).
func watchFailure(err error) (int, errorResponse) {
	var up *upstreamError
	if errors.As(err, &up) {
		switch {
		case up.code == "watch_cap":
			return http.StatusConflict, errorResponse{Error: up.text, Code: "watch_cap"}
		case up.code == "platform_disconnected" || up.code == "platform_ambiguous":
			return http.StatusConflict, errorResponse{Error: "Synaplan does not know this OpenCloud yet. Ask your administrator.", Code: "cloud_not_linked"}
		case up.code == "feature_not_configured":
			return http.StatusNotFound, errorResponse{Error: "Watching folders is turned off in Synaplan. Ask your administrator.", Code: "watches_off"}
		case up.status == http.StatusBadRequest && up.text != "":
			return http.StatusBadRequest, errorResponse{Error: up.text, Code: "watch_invalid"}
		}
	}
	return http.StatusBadGateway, errorResponse{Error: "Synaplan could not finish this. Try again in a minute, or ask your administrator.", Code: "synaplan_error"}
}

// writeWatchFailure logs err and answers with watchFailure.
func writeWatchFailure(w http.ResponseWriter, op string, err error) {
	log.Printf("%s: %v", op, err)
	status, body := watchFailure(err)
	writeJSON(w, status, body)
}

// requireTargetID reads targetId from the query string, answering 400 when
// it is absent or blank.
func requireTargetID(w http.ResponseWriter, r *http.Request) (string, bool) {
	targetID := strings.TrimSpace(r.URL.Query().Get("targetId"))
	if targetID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "targetId is required"})
		return "", false
	}
	return targetID, true
}

// WatchOptions lists what the watch dialog can offer: the person's tasks
// and knowledge folders, whether the item is watched already (and in which
// state), and whether files are sent while the person is away.
func (h *Handler) WatchOptions(w http.ResponseWriter, r *http.Request) {
	if h.refuseWatchSharedKey(w) {
		return
	}
	targetID, ok := requireTargetID(w, r)
	if !ok {
		return
	}
	resp, err := h.synaplanAPI.CloudWatchOptionsWithResponse(r.Context())
	if err != nil {
		writeWatchFailure(w, "watch options", err)
		return
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		writeWatchFailure(w, "watch options", upstream(resp.StatusCode(), resp.Body))
		return
	}
	watches, err := h.watchesOn(r.Context(), targetID)
	if err != nil {
		writeWatchFailure(w, "watch options", err)
		return
	}
	type task struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	tasks := make([]task, 0, len(resp.JSON200.Tasks))
	for _, t := range resp.JSON200.Tasks {
		tasks = append(tasks, task{ID: t.Id, Name: t.Name})
	}
	folders := make([]string, 0, len(resp.JSON200.Folders))
	for _, f := range resp.JSON200.Folders {
		folders = append(folders, f.GroupKey)
	}
	blocked := h.uploadsBlocked(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"tasks":          tasks,
		"folders":        folders,
		"watching":       len(watches) > 0,
		"watchState":     watchStateOf(watches),
		"uploadsEnabled": blocked == "",
		"uploadsBlocked": blocked,
	})
}

// watchStateOf summarises the watches on one target: active wins over
// attention, which wins over paused. Empty when there are none.
func watchStateOf(watches []watch) string {
	if len(watches) == 0 {
		return ""
	}
	state := watchStatePaused
	for _, each := range watches {
		if each.State == watchStateActive {
			return watchStateActive
		}
		if each.State == watchStateAttention {
			state = watchStateAttention
		}
	}
	return state
}

type watchStartRequest struct {
	TargetKind string `json:"targetKind"`
	TargetID   string `json:"targetId"`
	Preset     string `json:"preset"`
	GroupKey   string `json:"groupKey"`
	TaskID     int    `json:"taskId"`
}

// validate checks a start request before anything is read or sent.
func (req *watchStartRequest) validate() string {
	req.TargetID = strings.TrimSpace(req.TargetID)
	req.GroupKey = strings.TrimSpace(req.GroupKey)
	switch {
	case req.TargetKind != targetFolder && req.TargetKind != targetFile, req.TargetID == "":
		return "Choose a folder or a file."
	case req.Preset != presetSearchable && req.Preset != presetTask:
		return "Choose what should happen."
	case req.Preset == presetSearchable && req.GroupKey == "":
		return "Choose a knowledge folder."
	case req.Preset == presetSearchable && len(req.GroupKey) > maxGroupKey:
		return "The knowledge folder name is too long."
	case req.Preset == presetTask && req.TaskID <= 0:
		return "Choose one of your tasks."
	}
	return ""
}

// WatchStart asks Synaplan to watch one folder or one file. The item is
// read first, as the signed-in person, so a watch is only started on
// something they can open, and Synaplan gets its path to show.
func (h *Handler) WatchStart(w http.ResponseWriter, r *http.Request) {
	if h.refuseWatchSharedKey(w) {
		return
	}
	var req watchStartRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	if problem := req.validate(); problem != "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: problem})
		return
	}
	target, err := h.cs3.StatResource(r.Context(), req.TargetID)
	if err != nil {
		log.Printf("watch start: stat: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "This file is no longer there. Reload the folder and try again.",
			Code:  "file_not_found",
		})
		return
	}
	if target.IsFolder != (req.TargetKind == targetFolder) {
		writeJSON(w, http.StatusBadRequest, errorResponse{
			Error: "Choose a folder or a file.",
			Code:  "watch_invalid",
		})
		return
	}
	body := map[string]any{
		"target_kind": req.TargetKind,
		"target_id":   req.TargetID,
		"target_path": target.Path,
		"preset":      req.Preset,
	}
	if req.Preset == presetTask {
		body["task_id"] = req.TaskID
	} else {
		body["group_key"] = req.GroupKey
	}
	payload, err := withJSON(body)
	if err != nil {
		writeWatchFailure(w, "watch start", err)
		return
	}
	resp, err := h.synaplanAPI.CloudWatchCreateWithResponse(r.Context(), payload)
	if err != nil {
		writeWatchFailure(w, "watch start", err)
		return
	}
	if resp.StatusCode() != http.StatusOK {
		writeWatchFailure(w, "watch start", upstream(resp.StatusCode(), resp.Body))
		return
	}
	var created watch
	if err := json.Unmarshal(resp.Body, &created); err != nil {
		writeWatchFailure(w, "watch start", fmt.Errorf("decoding the new watch: %w", err))
		return
	}
	blocked := h.uploadsBlocked(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"id":             created.ID,
		"uploadsEnabled": blocked == "",
		"uploadsBlocked": blocked,
	})
}

// WatchStop stops every watch the signed-in person has on one folder or
// file.
func (h *Handler) WatchStop(w http.ResponseWriter, r *http.Request) {
	if h.refuseWatchSharedKey(w) {
		return
	}
	targetID, ok := requireTargetID(w, r)
	if !ok {
		return
	}
	watches, err := h.watchesOn(r.Context(), targetID)
	if err != nil {
		writeWatchFailure(w, "watch stop", err)
		return
	}
	stopped := 0
	for _, each := range watches {
		resp, err := h.synaplanAPI.CloudWatchStopWithResponse(r.Context(), strconv.Itoa(each.ID))
		if err != nil {
			writeWatchFailure(w, "watch stop", err)
			return
		}
		switch resp.StatusCode() {
		case http.StatusOK:
			stopped++
		case http.StatusNotFound:
		default:
			writeWatchFailure(w, "watch stop", upstream(resp.StatusCode(), resp.Body))
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"stopped": stopped})
}

// watches lists the person's watches that are not stopped, as ctx's
// credential names them: the signed-in person, or one who is away.
func (h *Handler) watches(ctx context.Context) ([]watch, error) {
	resp, err := h.synaplanAPI.CloudWatchesWithResponse(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing watches: %w", err)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, upstream(resp.StatusCode(), resp.Body)
	}
	var page struct {
		Items []watch `json:"items"`
	}
	if err := json.Unmarshal(resp.Body, &page); err != nil {
		return nil, fmt.Errorf("decoding the watch list: %w", err)
	}
	return page.Items, nil
}

// watchesOn is the person's watches on one folder or file.
func (h *Handler) watchesOn(ctx context.Context, targetID string) ([]watch, error) {
	all, err := h.watches(ctx)
	if err != nil {
		return nil, err
	}
	var on []watch
	for _, each := range all {
		if each.TargetID == targetID {
			on = append(on, each)
		}
	}
	return on, nil
}

// refuseWatchSharedKey closes the watch routes when every OpenCloud user
// shares one Synaplan key. That key's watches belong to one Synaplan
// account, so any signed-in user could list or stop them by target id.
func (h *Handler) refuseWatchSharedKey(w http.ResponseWriter) bool {
	if h == nil || !h.sharedKey {
		return false
	}
	writeJSON(w, http.StatusForbidden, errorResponse{
		Error: "Watching needs each person signed in to Synaplan. This installation uses one shared key.",
		Code:  "watch_shared_key",
	})
	return true
}

// awayAllowance returns the signed-in person's subject when this service
// works for them while they are away, or "" and the reason ("machine_auth"
// or "consent") when it cannot.
func (h *Handler) awayAllowance(ctx context.Context) (subject, blocked string) {
	if h == nil || h.cs3 == nil || !h.cs3.CanImpersonate() || !h.delegation.Configured() {
		return "", blockedMachineAuth
	}
	sub, err := h.signedInSubject(ctx)
	if err != nil {
		log.Printf("away: who is signed in: %v", err)
		return "", blockedConsent
	}
	connected, err := h.delegation.Connected(ctx, sub)
	if err != nil {
		log.Printf("away: allowed while away: %v", err)
		return "", blockedConsent
	}
	if !connected {
		return "", blockedConsent
	}
	return sub, ""
}

// uploadsBlocked says why files are not sent while the person is away.
// machine_auth means the service cannot read files without a browser
// session, so asking the person to allow it again would not help.
// consent means they have not allowed Synaplan to work for them.
// An empty string means uploads while they are away are on.
func (h *Handler) uploadsBlocked(ctx context.Context) string {
	_, blocked := h.awayAllowance(ctx)
	return blocked
}
