package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
)

// errReadsEverything is a request to stop reading one folder while the
// person lets Synaplan read every folder.
var errReadsEverything = errors.New("synaplan may read every folder")

// errOutsidePersonal is a request to name a folder that is not in the
// person's personal space. Synaplan stores the grant as a relative path,
// which would otherwise name the same path in every space.
var errOutsidePersonal = errors.New("folder is outside the personal space")

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
// their folders. Synaplan keeps the setting. A cloud token cannot list the
// person's links, so this process also remembers the last scope it reported.
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
	personal, err := h.cs3.PersonalSpaceKey(r.Context())
	if err != nil {
		writeReadScopeFailure(w, "read scope", err)
		return
	}
	scope, err := h.viewReadScope(r.Context(), sub)
	if err != nil {
		writeReadScopeFailure(w, "read scope", err)
		return
	}
	writeJSON(w, http.StatusOK, readScopeAnswerFor(h, r.Context(), scope, folder, personal))
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
	personal, err := h.cs3.PersonalSpaceKey(r.Context())
	if err != nil {
		writeReadScopeFailure(w, "read scope", err)
		return
	}
	if !inPersonalSpace(personal, folder.ID) {
		writeReadScopeFailure(w, "read scope", errOutsidePersonal)
		return
	}
	sub, err := h.signedInSubject(r.Context())
	if err != nil {
		writeReadScopeFailure(w, "read scope", err)
		return
	}
	next, err := h.commitReadScope(r.Context(), sub, personal, folder, req.Allow)
	if err != nil {
		writeReadScopeFailure(w, "read scope", err)
		return
	}
	writeJSON(w, http.StatusOK, readScopeAnswerFor(h, r.Context(), next, folder, personal))
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

// scopeBook loads and saves one person's read scope. The Synaplan book is
// the production one; tests substitute a book that does not leave the process.
type scopeBook interface {
	Load(ctx context.Context, sub string, op *scopeOp) (readScope, error)
	Save(ctx context.Context, sub string, scope readScope) error
}

// scopeOp is one person's read-scope lock and the last scope this process
// knows for them. known is only read or written while the lock is held.
// The lock is one token: a second update for the same person waits until
// the first has reported.
type scopeOp struct {
	gate  chan struct{}
	known readScope
	has   bool
}

// lockReadScope holds subject's lock and returns its unlock. The entry stays
// for the life of the process so the remembered scope survives between requests.
func (h *Handler) lockReadScope(subject string) (*scopeOp, func()) {
	h.scopeMu.Lock()
	if h.scopeOps == nil {
		h.scopeOps = map[string]*scopeOp{}
	}
	op := h.scopeOps[subject]
	if op == nil {
		op = &scopeOp{gate: make(chan struct{}, 1)}
		h.scopeOps[subject] = op
	}
	h.scopeMu.Unlock()
	op.gate <- struct{}{}
	return op, func() { <-op.gate }
}

func (h *Handler) book() scopeBook {
	if h != nil && h.scopes != nil {
		return h.scopes
	}
	return synaplanScopeBook{h: h}
}

// viewReadScope is the scope the dialog shows. It shares the change lock
// so it cannot observe a half-applied update.
func (h *Handler) viewReadScope(ctx context.Context, sub string) (readScope, error) {
	op, unlock := h.lockReadScope(sub)
	defer unlock()
	return h.book().Load(ctx, sub, op)
}

// commitReadScope reads the person's scope, applies folder, and reports
// the result, all under their lock. A second update waits, then reads
// what the first one reported, so it cannot put back a folder the first
// one removed.
func (h *Handler) commitReadScope(ctx context.Context, sub, personal string, folder cs3reader.Resource, allow bool) (readScope, error) {
	op, unlock := h.lockReadScope(sub)
	defer unlock()
	current, err := h.book().Load(ctx, sub, op)
	if err != nil {
		return readScope{}, err
	}
	next, err := withFolder(current, folder, personal, allow)
	if err != nil {
		return readScope{}, err
	}
	if err := h.book().Save(ctx, sub, next); err != nil {
		return readScope{}, err
	}
	op.known, op.has = next, true
	return next, nil
}

// synaplanScopeBook reads and reports the scope through the generated client.
type synaplanScopeBook struct {
	h *Handler
}

// Load reads the scope Synaplan keeps. A cloud token may not list the
// person's links (that list includes every cloud, and Synaplan answers
// insufficient_scope), so this process uses the scope it last reported.
// A person it has not reported for yet has the default: only what they watch.
func (b synaplanScopeBook) Load(ctx context.Context, sub string, op *scopeOp) (readScope, error) {
	resp, err := b.h.synaplanAPI.ListMyPlatformLinks(ctx)
	if err != nil {
		return readScope{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return readScope{}, err
	}
	if resp.StatusCode == http.StatusForbidden && upstream(resp.StatusCode, body).code == "insufficient_scope" {
		if op.has {
			return op.known, nil
		}
		return readScope{Mode: scopeWatched, Folders: []scopeFolder{}}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return readScope{}, upstream(resp.StatusCode, body)
	}
	scope, err := linkedReadScope(body, sub)
	if err != nil {
		return readScope{}, err
	}
	op.known, op.has = scope, true
	return scope, nil
}

func (b synaplanScopeBook) Save(ctx context.Context, sub string, scope readScope) error {
	return b.h.reportReadScope(ctx, sub, scope)
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
			path = normalizePath(path)
			if path == "" {
				continue
			}
			scope.Folders = append(scope.Folders, scopeFolder{Path: path})
		}
		break
	}
	return scope, nil
}

// withFolder is scope with folder allowed or no longer named. Folders the
// person named before keep their path, which is what Synaplan matches a
// task's folder step by. A scope that names folders reads them on top of
// the watched ones; one that names none falls back to the watched ones.
// personal is the person's personal space. A folder anywhere else shares
// only a relative path, so it neither creates a grant nor removes one.
func withFolder(scope readScope, folder cs3reader.Resource, personal string, allow bool) (readScope, error) {
	if scope.Mode == scopeAll {
		if allow {
			return scope, nil
		}
		return readScope{}, errReadsEverything
	}
	if allow && !inPersonalSpace(personal, folder.ID) {
		return readScope{}, errOutsidePersonal
	}
	next := readScope{Folders: []scopeFolder{}}
	for _, named := range scope.Folders {
		if !sameNamed(named, folder, personal) {
			next.Folders = append(next.Folders, named)
		}
	}
	if allow {
		next.Folders = append(next.Folders, scopeFolder{ID: folder.ID, Path: normalizePath(folder.Path)})
	}
	next.Mode = scopeWatched
	if len(next.Folders) > 0 {
		next.Mode = scopeAllowed
	}
	return next, nil
}

// sameNamed reports whether named is the grant for folder. Path grants are
// personal-space paths, so a folder in another space is never the same grant.
func sameNamed(named scopeFolder, folder cs3reader.Resource, personal string) bool {
	if !inPersonalSpace(personal, folder.ID) {
		return false
	}
	if named.ID != "" && named.ID == folder.ID {
		return true
	}
	path := normalizePath(named.Path)
	return path != "" && path == normalizePath(folder.Path)
}

// inPersonalSpace reports whether resourceID lives in personal ("storage$space").
func inPersonalSpace(personal, resourceID string) bool {
	return personal != "" && cs3reader.SpaceKey(resourceID) == personal
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

// readScopeAnswerFor is what the dialog shows for folder. A path grant
// names a folder in personal only, so the same relative path elsewhere
// is not this grant.
func readScopeAnswerFor(h *Handler, ctx context.Context, scope readScope, folder cs3reader.Resource, personal string) readScopeAnswer {
	answer := readScopeAnswer{Mode: scope.Mode, ReadsWhileAway: h.uploadsBlocked(ctx) == ""}
	for _, named := range scope.Folders {
		if sameNamed(named, folder, personal) {
			answer.Named = true
		}
	}
	answer.Allowed = scopeAllows(scope, nil, folder, personal)
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
	case errors.Is(err, errOutsidePersonal):
		return http.StatusConflict, errorResponse{Error: "Synaplan can only read folders in your personal space.", Code: "outside_personal"}
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
