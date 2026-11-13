package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
)

// errReadsEverything is a request to stop reading one folder while the
// person lets Synaplan read every folder.
var errReadsEverything = errors.New("synaplan may read every folder")

// readScopeAnswer is what the read-folder dialog shows for one folder.
type readScopeAnswer struct {
	Mode string `json:"mode"`
	// Named: the person allowed this folder itself, so they can stop it.
	Named bool `json:"named"`
	// Allowed: Synaplan may read the folder, because it is named, lies in
	// a named folder, or everything may be read.
	Allowed bool `json:"allowed"`
	// ReadsWhileAway: Synaplan's reads run while the person is away, so
	// they need the person's permission for that and machine auth.
	ReadsWhileAway bool `json:"readsWhileAway"`
}

// readScopeChange is a request to allow or stop reading one folder.
type readScopeChange struct {
	FolderID string `json:"folderId"`
	Allow    bool   `json:"allow"`
}

// ReadScope tells the signed-in person whether Synaplan may read one of
// their folders. Synaplan keeps the setting; this service keeps none.
func (h *Handler) ReadScope(w http.ResponseWriter, r *http.Request) {
	folderID := strings.TrimSpace(r.URL.Query().Get("folderId"))
	if folderID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "folderId is required"})
		return
	}
	folder, ok := h.statFolder(w, r.Context(), folderID)
	if !ok {
		return
	}
	sub, err := h.signedInSubject(r.Context())
	if err != nil {
		writeReadScopeFailure(w, "read scope", err)
		return
	}
	scope, ok := h.currentReadScope(w, r.Context(), sub)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.readScopeAnswer(r.Context(), scope, folder))
}

// SetReadScope allows Synaplan to read one folder, or stops it, and reports
// the person's new setting to Synaplan. The folder's path comes from
// OpenCloud, never from the browser, so a name cannot widen what Synaplan
// may read.
func (h *Handler) SetReadScope(w http.ResponseWriter, r *http.Request) {
	var req readScopeChange
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	req.FolderID = strings.TrimSpace(req.FolderID)
	if req.FolderID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Choose a folder."})
		return
	}
	folder, ok := h.statFolder(w, r.Context(), req.FolderID)
	if !ok {
		return
	}
	sub, err := h.signedInSubject(r.Context())
	if err != nil {
		writeReadScopeFailure(w, "read scope", err)
		return
	}
	current, ok := h.currentReadScope(w, r.Context(), sub)
	if !ok {
		return
	}
	next, err := withFolder(current, folder, req.Allow)
	if err != nil {
		writeReadScopeFailure(w, "read scope", err)
		return
	}
	if err := h.reportReadScope(r.Context(), sub, next); err != nil {
		writeReadScopeFailure(w, "read scope", err)
		return
	}
	writeJSON(w, http.StatusOK, h.readScopeAnswer(r.Context(), next, folder))
}

// statFolder reads the folder as the signed-in person, so only a folder
// they can open is asked about or allowed, and its path is OpenCloud's.
func (h *Handler) statFolder(w http.ResponseWriter, ctx context.Context, folderID string) (cs3reader.Resource, bool) {
	folder, err := h.cs3.StatResource(ctx, folderID)
	if err != nil || !folder.IsFolder {
		log.Printf("read scope: stat: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "This file is no longer there. Reload the folder and try again.",
			Code:  "file_not_found",
		})
		return cs3reader.Resource{}, false
	}
	return folder, true
}

// currentReadScope is the signed-in person's setting as Synaplan keeps it,
// read from their link to this OpenCloud. A person Synaplan has no link
// for yet has the default: only what they watch.
func (h *Handler) currentReadScope(w http.ResponseWriter, ctx context.Context, sub string) (readScope, bool) {
	resp, err := h.synaplanAPI.ListMyPlatformLinksWithResponse(ctx)
	if err != nil {
		writeReadScopeFailure(w, "read scope", err)
		return readScope{}, false
	}
	if resp.StatusCode() != http.StatusOK {
		writeReadScopeFailure(w, "read scope", upstream(resp.StatusCode(), resp.Body))
		return readScope{}, false
	}
	scope, err := linkedReadScope(resp.Body, sub)
	if err != nil {
		writeReadScopeFailure(w, "read scope", err)
		return readScope{}, false
	}
	return scope, true
}

// linkedReadScope picks the read scope of sub's link to an OpenCloud out of
// Synaplan's list of the person's links. Synaplan lists the named folders
// by path only.
func linkedReadScope(body []byte, sub string) (readScope, error) {
	var page struct {
		Links []struct {
			Client     string `json:"client"`
			ExternalID string `json:"external_id"`
			Status     string `json:"status"`
			ReadScope  *struct {
				Mode    string   `json:"mode"`
				Folders []string `json:"folders"`
			} `json:"read_scope"`
		} `json:"links"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return readScope{}, fmt.Errorf("decoding the link list: %w", err)
	}
	scope := readScope{Mode: scopeWatched, Folders: []scopeFolder{}}
	for _, link := range page.Links {
		if link.Client != "opencloud" || link.ExternalID != sub || link.Status != "active" || link.ReadScope == nil {
			continue
		}
		if link.ReadScope.Mode != "" {
			scope.Mode = link.ReadScope.Mode
		}
		for _, path := range link.ReadScope.Folders {
			scope.Folders = append(scope.Folders, scopeFolder{Path: strings.Trim(path, "/")})
		}
		break
	}
	return scope, nil
}

// withFolder is scope with folder allowed or no longer named. Folders the
// person named before keep their path, which is what Synaplan matches a
// task's folder step by. A scope that names folders reads them on top of
// the watched ones; one that names none falls back to the watched ones.
func withFolder(scope readScope, folder cs3reader.Resource, allow bool) (readScope, error) {
	if scope.Mode == scopeAll {
		if allow {
			return scope, nil
		}
		return readScope{}, errReadsEverything
	}
	next := readScope{Folders: []scopeFolder{}}
	for _, named := range scope.Folders {
		if named.Path != folder.Path && (named.ID == "" || named.ID != folder.ID) {
			next.Folders = append(next.Folders, named)
		}
	}
	if allow {
		next.Folders = append(next.Folders, scopeFolder{ID: folder.ID, Path: folder.Path})
	}
	next.Mode = scopeWatched
	if len(next.Folders) > 0 {
		next.Mode = scopeAllowed
	}
	return next, nil
}

func (h *Handler) reportReadScope(ctx context.Context, sub string, scope readScope) error {
	raw, err := json.Marshal(map[string]any{"external_id": sub, "mode": scope.Mode, "folders": scope.Folders})
	if err != nil {
		return err
	}
	resp, err := h.synaplanAPI.ReportCloudReadScopeWithBodyWithResponse(ctx, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if resp.StatusCode() != http.StatusOK {
		return upstream(resp.StatusCode(), resp.Body)
	}
	return nil
}

func (h *Handler) readScopeAnswer(ctx context.Context, scope readScope, folder cs3reader.Resource) readScopeAnswer {
	answer := readScopeAnswer{Mode: scope.Mode, ReadsWhileAway: h.worksWhileAway(ctx)}
	for _, named := range scope.Folders {
		if named.Path == folder.Path || (named.ID != "" && named.ID == folder.ID) {
			answer.Named = true
		}
	}
	answer.Allowed = scopeAllows(scope, nil, folder)
	return answer
}

// readScopeFailure is what a person sees when reading or changing the
// setting fails: one sentence and a code the extension translates.
func readScopeFailure(err error) (int, errorResponse) {
	var up *upstreamError
	switch {
	case errors.Is(err, errNoDelegation):
		return http.StatusConflict, errorResponse{Error: "Synaplan cannot read folders in this OpenCloud. Ask your administrator.", Code: "reads_unavailable"}
	case errors.Is(err, errReadsEverything):
		return http.StatusConflict, errorResponse{Error: "Synaplan may read all your folders, so one cannot be left out.", Code: "reads_everything"}
	case errors.As(err, &up):
		switch up.code {
		case "platform_disconnected", "platform_ambiguous", "instance_pending", "unknown_identity":
			return http.StatusConflict, errorResponse{Error: "Synaplan does not know this OpenCloud yet. Ask your administrator.", Code: "cloud_not_linked"}
		}
	}
	return http.StatusBadGateway, errorResponse{Error: "Synaplan could not finish this. Try again in a minute, or ask your administrator.", Code: "synaplan_error"}
}

// writeReadScopeFailure logs err and answers with readScopeFailure.
func writeReadScopeFailure(w http.ResponseWriter, op string, err error) {
	log.Printf("%s: %v", op, err)
	status, body := readScopeFailure(err)
	writeJSON(w, status, body)
}
