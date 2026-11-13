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
	"strconv"
	"strings"
	"time"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/delegation"
	"github.com/metadist/synaplan-opencloud/internal/notify"
)

// pickupTimeout bounds one folder read. Synaplan keeps a pickup open for
// ten minutes and gives up on its notification after thirty seconds, so the
// read runs after the answer, on its own deadline.
const pickupTimeout = 5 * time.Minute

// Read scope modes, as Synaplan stores them: only folders the person
// watches, those plus the folders they named, or everything.
const (
	scopeWatched = "watched"
	scopeAllowed = "allowed"
	scopeAll     = "all"
)

// readScope is what a person lets Synaplan read.
type readScope struct {
	Mode    string        `json:"mode"`
	Folders []scopeFolder `json:"folders"`
}

// scopeFolder is one folder a person named, by id and by its path in their
// personal space. Synaplan matches a task's folder step by the path.
type scopeFolder struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// pickupFiles is how a folder read reaches the person's files: by id, and
// by the path a task's folder step names.
type pickupFiles interface {
	watchFiles
	StatPath(ctx context.Context, relPath string) (cs3reader.Resource, error)
}

// pickupClaim is what Synaplan hands the cloud that claimed a pickup.
type pickupClaim struct {
	ExternalID string `json:"external_id"`
	Selector   struct {
		FolderID   string `json:"folder_id"`
		FolderPath string `json:"folder_path"`
	} `json:"selector"`
	ReadScope readScope `json:"read_scope"`
}

// pickupSkip is a file Synaplan asked for that was not sent.
type pickupSkip struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// pickupResult closes a pickup in Synaplan.
type pickupResult struct {
	Status    string       `json:"status"`
	Listed    int          `json:"listed"`
	Sent      int          `json:"sent"`
	Skipped   []pickupSkip `json:"skipped"`
	ErrorCode string       `json:"error_code,omitempty"`
}

// listingItem is one file of the listing Synaplan picks from.
type listingItem struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Name    string `json:"name"`
	Folder  string `json:"folder"`
	Size    uint64 `json:"size"`
	Mime    string `json:"mime"`
}

// startPickup answers a verified pickup notification: Synaplan wants to read
// a folder of the person it names. The person is away, so the read runs as
// them, with their own Synaplan token and a reva token from machine auth,
// after the answer. When it cannot start, the notification is forgotten so
// a retry is handled.
func (h *Handler) startPickup(w http.ResponseWriter, r *http.Request, claims notify.Claims) {
	id, err := strconv.Atoi(claims.ID)
	if err != nil || id <= 0 || claims.Subject == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid_pickup"})
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), pickupTimeout)
	away, err := h.actAs(ctx, claims.Subject)
	if err != nil {
		cancel()
		h.forgetHandled(claims)
		if errors.Is(err, delegation.ErrNotConnected) {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "not_connected"})
			return
		}
		log.Printf("pickup %d: acting for the person: %v", id, err)
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "files_unreachable"})
		return
	}
	go func() {
		defer cancel()
		h.runPickup(away, h.cs3, claims.Subject, id)
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"state": "pending"})
}

// runPickup claims the pickup, lists the folder it names, sends the files
// Synaplan asks for and closes it. ctx works as sub: Synaplan sees their
// token, the gateway their reva token.
func (h *Handler) runPickup(ctx context.Context, files pickupFiles, sub string, id int) pickupResult {
	claim, err := h.claimPickup(ctx, id)
	if err != nil {
		// Not claimed: another replica has it, it expired, or Synaplan did
		// not answer. Nothing here may report on it.
		log.Printf("pickup %d: claim: %v", id, err)
		return pickupResult{}
	}
	if claim.ExternalID != sub {
		return h.finishPickup(ctx, id, failedPickup("not_allowed"))
	}
	folder, err := pickupFolder(ctx, files, claim.Selector.FolderID, claim.Selector.FolderPath)
	if err != nil {
		log.Printf("pickup %d: folder: %v", id, err)
		if errors.Is(err, cs3reader.ErrNotFound) {
			return h.finishPickup(ctx, id, failedPickup("not_found"))
		}
		return h.finishPickup(ctx, id, failedPickup("unreachable"))
	}
	if !scopeAllows(claim.ReadScope, h.watchedFolders(ctx, claim.ReadScope), folder) {
		return h.finishPickup(ctx, id, failedPickup("not_allowed"))
	}
	children, err := files.List(ctx, folder.ID)
	if err != nil {
		log.Printf("pickup %d: list: %v", id, err)
		return h.finishPickup(ctx, id, failedPickup("unreachable"))
	}
	byID := map[string]cs3reader.Resource{}
	items := []listingItem{}
	for _, child := range children {
		if child.IsFolder || child.ID == "" {
			continue
		}
		byID[child.ID] = child
		items = append(items, listingItem{ID: child.ID, Version: child.Etag, Name: child.Name, Folder: folder.Path, Size: child.Size, Mime: child.MimeType})
	}
	upload, err := h.listPickup(ctx, id, items)
	if err != nil {
		log.Printf("pickup %d: listing: %v", id, err)
		result := failedPickup("unreachable")
		result.Listed = len(items)
		return h.finishPickup(ctx, id, result)
	}
	result := pickupResult{Listed: len(items), Skipped: []pickupSkip{}}
	for _, fileID := range upload {
		file, ok := byID[fileID]
		if !ok {
			result.Skipped = append(result.Skipped, pickupSkip{ID: fileID, Reason: "not_found"})
			continue
		}
		if err := h.uploadPickupFile(ctx, files, id, file); err != nil {
			log.Printf("pickup %d: %v", id, err)
			result.Skipped = append(result.Skipped, pickupSkip{ID: fileID, Reason: "unreachable"})
			continue
		}
		result.Sent++
	}
	switch {
	case len(result.Skipped) == 0:
		result.Status = "done"
	case result.Sent > 0:
		result.Status = "partial"
	default:
		result.Status = "failed"
		result.ErrorCode = "unreachable"
	}
	return h.finishPickup(ctx, id, result)
}

func failedPickup(code string) pickupResult {
	return pickupResult{Status: "failed", Skipped: []pickupSkip{}, ErrorCode: code}
}

// pickupFolder is the folder a pickup names, as OpenCloud knows it. A
// task's folder step names a path in the person's space, which only the
// cloud can turn into an id; the path the folder really has is read back by
// id, never taken from the request.
func pickupFolder(ctx context.Context, files pickupFiles, folderID, folderPath string) (cs3reader.Resource, error) {
	if folderID == "" {
		if strings.Trim(folderPath, "/") == "" {
			return cs3reader.Resource{}, fmt.Errorf("the pickup names no folder: %w", cs3reader.ErrNotFound)
		}
		found, err := files.StatPath(ctx, folderPath)
		if err != nil {
			return cs3reader.Resource{}, err
		}
		folderID = found.ID
	}
	folder, err := files.StatResource(ctx, folderID)
	if err != nil {
		return cs3reader.Resource{}, err
	}
	if !folder.IsFolder {
		return cs3reader.Resource{}, fmt.Errorf("%s is not a folder: %w", folderID, cs3reader.ErrNotFound)
	}
	return folder, nil
}

// watchedFolders is the folders the person watches now, when their scope
// depends on it. A failed watch list counts as no watches: it can only
// refuse a read, never allow one.
func (h *Handler) watchedFolders(ctx context.Context, scope readScope) map[string]bool {
	if scope.Mode == scopeAll {
		return nil
	}
	all, err := h.watches(ctx)
	if err != nil {
		log.Printf("pickup: watch list: %v", err)
		return nil
	}
	watched := map[string]bool{}
	for _, each := range all {
		if each.State == "active" && each.TargetKind == "folder" {
			watched[each.TargetID] = true
		}
	}
	return watched
}

// scopeAllows is this cloud's side of "what Synaplan may read". A signed
// notification proves Synaplan asked, not where the folder it names lives,
// so the folder's real id and path decide. A folder the person watches is
// always allowed; one they named, and the folders below it, in "allowed"
// mode; everything in "all".
func scopeAllows(scope readScope, watched map[string]bool, folder cs3reader.Resource) bool {
	if folder.ID == "" {
		return false
	}
	switch scope.Mode {
	case scopeAll:
		return true
	case scopeAllowed:
		for _, named := range scope.Folders {
			if named.ID != "" && named.ID == folder.ID {
				return true
			}
			path := strings.Trim(named.Path, "/")
			if path != "" && (folder.Path == path || strings.HasPrefix(folder.Path, path+"/")) {
				return true
			}
		}
		return watched[folder.ID]
	case "", scopeWatched:
		return watched[folder.ID]
	default:
		return false
	}
}

func (h *Handler) claimPickup(ctx context.Context, id int) (pickupClaim, error) {
	resp, err := h.synaplanAPI.ClaimCloudPickupWithResponse(ctx, id)
	if err != nil {
		return pickupClaim{}, err
	}
	if resp.StatusCode() != http.StatusOK {
		return pickupClaim{}, upstream(resp.StatusCode(), resp.Body)
	}
	var claim pickupClaim
	if err := json.Unmarshal(resp.Body, &claim); err != nil {
		return pickupClaim{}, fmt.Errorf("decoding the claim: %w", err)
	}
	return claim, nil
}

// listPickup sends the listing and returns the ids Synaplan wants uploaded.
func (h *Handler) listPickup(ctx context.Context, id int, items []listingItem) ([]string, error) {
	raw, err := json.Marshal(map[string]any{"items": items})
	if err != nil {
		return nil, err
	}
	resp, err := h.synaplanAPI.ListCloudPickupWithBodyWithResponse(ctx, id, "application/json", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, upstream(resp.StatusCode(), resp.Body)
	}
	var decision struct {
		Upload []string `json:"upload"`
	}
	if err := json.Unmarshal(resp.Body, &decision); err != nil {
		return nil, fmt.Errorf("decoding the listing answer: %w", err)
	}
	return decision.Upload, nil
}

func (h *Handler) uploadPickupFile(ctx context.Context, files watchFiles, id int, file cs3reader.Resource) error {
	return uploadFile(ctx, files, file, func(contentType string, body io.Reader) (int, []byte, error) {
		resp, err := h.synaplanAPI.UploadCloudPickupFileWithBodyWithResponse(ctx, id, contentType, body)
		if err != nil {
			return 0, nil, err
		}
		return resp.StatusCode(), resp.Body, nil
	})
}

// finishPickup closes the pickup in Synaplan with result and returns it.
func (h *Handler) finishPickup(ctx context.Context, id int, result pickupResult) pickupResult {
	if err := h.recordPickup(ctx, id, result); err != nil {
		log.Printf("pickup %d: result: %v", id, err)
	}
	return result
}

func (h *Handler) recordPickup(ctx context.Context, id int, result pickupResult) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	resp, err := h.synaplanAPI.RecordCloudPickupResultWithBodyWithResponse(ctx, id, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if resp.StatusCode() != http.StatusOK {
		return upstream(resp.StatusCode(), resp.Body)
	}
	return nil
}
