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
	"github.com/metadist/synaplan-opencloud/internal/delegation"
)

// errReadsEverything is a request to stop reading one folder while the
// person lets Synaplan read every folder.
var errReadsEverything = errors.New("synaplan may read every folder")

// readScopeSetting names the person's read scope in the delegation store.
const readScopeSetting = "read-scope"

// errReadScopeMoved is a change that lost to another one in between. The
// lease makes this rare; trying again starts from the newer setting.
var errReadScopeMoved = errors.New("the read setting changed while it was being saved")

// errNotPersonal is a folder outside the person's personal space. A task's
// folder step names a path in that space, so a path from any other space
// would name a different folder there.
var errNotPersonal = errors.New("the folder is not in the personal space")

// readScopeAnswer is what the read-folder dialog shows for one folder.
type readScopeAnswer struct {
	Mode string `json:"mode"`
	// Named: the person allowed this folder itself, so they can stop it.
	Named bool `json:"named"`
	// Allowed: Synaplan may read the folder, because it is named, lies in
	// a named folder, or everything may be read.
	Allowed bool `json:"allowed"`
	// Blocked says why reads cannot run while the person is away:
	// machine_auth (no machine authentication configured), consent (they
	// have not allowed Synaplan to work for them), or empty when they can.
	Blocked string `json:"blocked"`
}

// readScopeChange is a request to allow or stop reading one folder.
type readScopeChange struct {
	FolderID string `json:"folderId"`
	Allow    bool   `json:"allow"`
}

// ReadScope tells the signed-in person whether Synaplan may read one of
// their folders. Synaplan only stores what this service reports and a
// cloud cannot read it back, so the setting this service keeps in the
// delegation store is the record.
func (h *Handler) ReadScope(w http.ResponseWriter, r *http.Request) {
	folderID := strings.TrimSpace(r.URL.Query().Get("folderId"))
	if folderID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "folderId is required"})
		return
	}
	if h.refuseReads(w) {
		return
	}
	folder, ok := h.statPersonalFolder(w, r.Context(), folderID)
	if !ok {
		return
	}
	sub, err := h.signedInSubject(r.Context())
	if err != nil {
		writeReadScopeFailure(w, "read scope", err)
		return
	}
	scope, _, err := h.keptReadScope(r.Context(), sub)
	if err != nil {
		writeReadScopeFailure(w, "read scope", err)
		return
	}
	writeJSON(w, http.StatusOK, h.readScopeAnswer(r.Context(), scope, folder))
}

// SetReadScope allows Synaplan to read one folder, or stops it, and reports
// the person's new setting to Synaplan. The folder's path comes from
// OpenCloud, never from the browser, so a name cannot widen what Synaplan
// may read. The change runs under the person's lease on every replica:
// it is reported first and kept second, so a failed report changes
// nothing and two changes reach Synaplan in the order they were kept.
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
	if h.refuseReads(w) {
		return
	}
	folder, ok := h.statPersonalFolder(w, r.Context(), req.FolderID)
	if !ok {
		return
	}
	sub, err := h.signedInSubject(r.Context())
	if err != nil {
		writeReadScopeFailure(w, "read scope", err)
		return
	}
	var next readScope
	err = h.delegation.WithSettingLease(r.Context(), readScopeSetting, sub, func(ctx context.Context) error {
		current, rev, err := h.keptReadScope(ctx, sub)
		if err != nil {
			return err
		}
		next, err = withFolder(current, folder, req.Allow)
		if err != nil {
			return err
		}
		if err := h.reportReadScope(ctx, sub, next); err != nil {
			return err
		}
		raw, err := json.Marshal(next)
		if err != nil {
			return err
		}
		kept, err := h.delegation.PutSetting(ctx, readScopeSetting, sub, raw, rev)
		if err != nil {
			return err
		}
		if !kept {
			return errReadScopeMoved
		}
		return nil
	})
	if err != nil {
		writeReadScopeFailure(w, "read scope change", err)
		return
	}
	writeJSON(w, http.StatusOK, h.readScopeAnswer(r.Context(), next, folder))
}

// refuseReads answers for an installation where Synaplan cannot read
// folders: a folder read runs while the person is away, which needs their
// own offline token, and the shared API key of Mode B has none. It runs
// before any I/O.
func (h *Handler) refuseReads(w http.ResponseWriter) bool {
	if h != nil && !h.sharedKey && h.delegation.Configured() && h.exchanger != nil {
		return false
	}
	status, body := readScopeFailure(errNoDelegation)
	writeJSON(w, status, body)
	return true
}

// statPersonalFolder reads the folder as the signed-in person, so only a
// folder they can open is asked about or allowed, its path is OpenCloud's,
// and it lies in their personal space.
func (h *Handler) statPersonalFolder(w http.ResponseWriter, ctx context.Context, folderID string) (cs3reader.Resource, bool) {
	folder, err := h.cs3.StatResource(ctx, folderID)
	if err == nil && !folder.IsFolder {
		err = fmt.Errorf("%s is not a folder: %w", folderID, cs3reader.ErrNotFound)
	}
	if err != nil {
		log.Printf("read scope: stat: %v", err)
		if errors.Is(err, cs3reader.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, errorResponse{
				Error: "This folder is no longer there. Reload the folder and try again.",
				Code:  "folder_not_found",
			})
			return cs3reader.Resource{}, false
		}
		writeReadScopeFailure(w, "read scope", err)
		return cs3reader.Resource{}, false
	}
	space, err := h.cs3.PersonalSpace(ctx)
	if err != nil {
		writeReadScopeFailure(w, "read scope", err)
		return cs3reader.Resource{}, false
	}
	if !cs3reader.InSpace(folder.ID, space) {
		writeReadScopeFailure(w, "read scope", errNotPersonal)
		return cs3reader.Resource{}, false
	}
	return folder, true
}

// keptReadScope is the person's setting as this service keeps it, and the
// revision to change it at. A person who never changed it has the default
// Synaplan starts a link with: only what they watch. A kept value that
// cannot be read is logged and treated as that default, so the next change
// replaces it instead of failing for good.
func (h *Handler) keptReadScope(ctx context.Context, sub string) (readScope, uint64, error) {
	fallback := readScope{Mode: scopeWatched, Folders: []scopeFolder{}}
	raw, rev, err := h.delegation.Setting(ctx, readScopeSetting, sub)
	if errors.Is(err, delegation.ErrUnreadable) {
		log.Printf("read scope: the kept setting cannot be read: %v", err)
		return fallback, rev, nil
	}
	if err != nil {
		return readScope{}, 0, err
	}
	if rev == 0 {
		return fallback, 0, nil
	}
	var scope readScope
	if err := json.Unmarshal(raw, &scope); err != nil {
		log.Printf("read scope: the kept setting is not JSON: %v", err)
		return fallback, rev, nil
	}
	if scope.Folders == nil {
		scope.Folders = []scopeFolder{}
	}
	if scope.Mode == "" {
		scope.Mode = scopeWatched
	}
	return scope, rev, nil
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
	answer := readScopeAnswer{Mode: scope.Mode, Blocked: h.uploadsBlocked(ctx)}
	for _, named := range scope.Folders {
		if named.Path == folder.Path || (named.ID != "" && named.ID == folder.ID) {
			answer.Named = true
		}
	}
	answer.Allowed = scopeAllows(scope, nil, folder, true)
	return answer
}

// readScopeFailure is what a person sees when reading or changing the
// setting fails: one sentence and a code the extension translates.
func readScopeFailure(err error) (int, errorResponse) {
	var up *upstreamError
	switch {
	case errors.Is(err, errNoDelegation):
		return http.StatusConflict, errorResponse{Error: "Synaplan cannot read folders in this OpenCloud. Ask your administrator.", Code: "reads_unavailable"}
	case errors.Is(err, errNotPersonal):
		return http.StatusConflict, errorResponse{Error: "Synaplan can only read folders in your personal space.", Code: "reads_personal_only"}
	case errors.Is(err, errReadScopeMoved):
		return http.StatusConflict, errorResponse{Error: "The setting was changed at the same time. Close this and try again.", Code: "reads_changed"}
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
