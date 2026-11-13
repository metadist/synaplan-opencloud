package handler

import (
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
	"github.com/metadist/synaplan-opencloud/internal/delegation"
)

const (
	// reconcileInterval spaces the passes over everyone's watches and kept
	// files. OpenCloud does not tell an extension about file changes, so a
	// pass is how a new or changed file is noticed.
	reconcileInterval = 10 * time.Minute
	// passTimeout bounds one pass, and one "check now".
	passTimeout = 8 * time.Minute
	// watchTimeout bounds the work for one watch, so one Synaplan or
	// gateway that hangs costs that watch, not the whole pass.
	watchTimeout = 2 * time.Minute
	// reconcileBatch is the most files Synaplan compares in one request.
	reconcileBatch = 500
)

// RunReconciler checks the watches and the files kept up to date of
// everyone who allowed Synaplan to work for them while they are away, now
// and every reconcileInterval, until ctx ends. It needs organisation login
// and OpenCloud's machine-auth key; without them it returns at once and
// files are only sent when a person checks themselves.
func (h *Handler) RunReconciler(ctx context.Context) {
	if !h.delegation.Configured() || !h.cs3.CanImpersonate() {
		return
	}
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for {
		pass, cancel := context.WithTimeout(ctx, passTimeout)
		h.reconcileEveryone(pass)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// reconcileEveryone runs one pass over every person with a kept token.
func (h *Handler) reconcileEveryone(ctx context.Context) {
	subjects, err := h.delegation.Subjects(ctx)
	if err != nil {
		log.Printf("reconcile: listing people: %v", err)
		return
	}
	for _, sub := range subjects {
		if ctx.Err() != nil {
			return
		}
		away, err := h.actAs(ctx, sub)
		if errors.Is(err, delegation.ErrNotConnected) {
			continue
		}
		if err != nil {
			log.Printf("reconcile: acting for a person: %v", err)
			continue
		}
		if _, err := h.reconcileWatches(away, h.cs3, ""); err != nil {
			log.Printf("reconcile: %v", err)
		}
		if h.synced != nil {
			if _, err := h.keepFilesCurrent(away, h.cs3, sub); err != nil {
				log.Printf("reconcile: kept files: %v", err)
			}
		}
	}
}

// actAs returns a context that works as sub while they are away: Synaplan
// sees their own token, minted from their offline token, and the CS3
// gateway a reva token for their OpenCloud user from machine auth. The
// user is found the way OpenCloud's proxy finds them at sign-in, by the
// claim it maps (PROXY_USER_OIDC_CLAIM to PROXY_USER_CS3_CLAIM).
func (h *Handler) actAs(ctx context.Context, sub string) (context.Context, error) {
	token, err := h.delegatedBearer(ctx, sub)
	if err != nil {
		return nil, err
	}
	value := delegation.Claim(token, h.userClaim)
	if value == "" {
		return nil, fmt.Errorf("the token names no %q claim", h.userClaim)
	}
	files, err := h.cs3.Impersonate(ctx, h.userAttr, value)
	if err != nil {
		return nil, err
	}
	return onBehalfOf(files, sub), nil
}

// WatchReconcileNow checks the signed-in person's watches on one folder or
// file now, with their own session, and sends Synaplan what it asks for.
func (h *Handler) WatchReconcileNow(w http.ResponseWriter, r *http.Request) {
	targetID := strings.TrimSpace(r.URL.Query().Get("targetId"))
	if targetID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "targetId is required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), passTimeout)
	defer cancel()
	result, err := h.reconcileWatches(ctx, h.cs3, targetID)
	if err != nil {
		writeWatchFailure(w, "watch check", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// reconcileResult counts what one check did.
type reconcileResult struct {
	Watches int `json:"watches"`
	Sent    int `json:"sent"`
	Failed  int `json:"failed"`
}

// reconcileWatches checks the active watches ctx's credential can see, or
// only those on targetID when it is set. A watch that fails is logged and
// counted; the others still run.
func (h *Handler) reconcileWatches(ctx context.Context, files watchFiles, targetID string) (reconcileResult, error) {
	var result reconcileResult
	all, err := h.watches(ctx)
	if err != nil {
		return result, err
	}
	for _, each := range all {
		if each.State != "active" || (targetID != "" && each.TargetID != targetID) {
			continue
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result.Watches++
		sent, failed, err := h.reconcileWatch(ctx, files, each)
		if err != nil {
			log.Printf("watch %d: %v", each.ID, err)
			failed++
		}
		result.Sent += sent
		result.Failed += failed
	}
	return result, nil
}

// reconcileWatch tells Synaplan which files the watch covers, at which
// version, and uploads the ones it asks for. Synaplan remembers each
// version it got, so asking again is safe: a pass that dies half way is
// finished by the next one, and two replicas never send a version twice.
func (h *Handler) reconcileWatch(ctx context.Context, files watchFiles, w watch) (sent, failed int, err error) {
	ctx, cancel := context.WithTimeout(ctx, watchTimeout)
	defer cancel()
	covered, err := watchedFiles(ctx, files, w)
	if err != nil {
		return 0, 0, err
	}
	for start := 0; start < len(covered); start += reconcileBatch {
		batch := covered[start:min(start+reconcileBatch, len(covered))]
		wanted, err := h.askReconcile(ctx, w.ID, batch)
		if err != nil {
			return sent, failed, err
		}
		for _, file := range batch {
			if !wanted[file.ID] {
				continue
			}
			if err := h.uploadWatchFile(ctx, files, w.ID, file); err != nil {
				log.Printf("watch %d: upload: %v", w.ID, err)
				failed++
				continue
			}
			sent++
		}
	}
	return sent, failed, nil
}

// watchedFiles is the watched file, or the files directly in the watched
// folder.
func watchedFiles(ctx context.Context, files watchFiles, w watch) ([]cs3reader.Resource, error) {
	if w.TargetKind == "file" {
		file, err := files.StatResource(ctx, w.TargetID)
		if err != nil {
			return nil, fmt.Errorf("stat: %w", err)
		}
		return []cs3reader.Resource{file}, nil
	}
	children, err := files.List(ctx, w.TargetID)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	out := children[:0]
	for _, child := range children {
		if !child.IsFolder {
			out = append(out, child)
		}
	}
	return out, nil
}

// askReconcile sends Synaplan the files and versions it covers and returns
// the ids it wants uploaded.
func (h *Handler) askReconcile(ctx context.Context, watchID int, batch []cs3reader.Resource) (map[string]bool, error) {
	type item struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	items := make([]item, 0, len(batch))
	for _, file := range batch {
		items = append(items, item{ID: file.ID, Version: file.Etag})
	}
	payload, err := withJSON(map[string]any{"items": items})
	if err != nil {
		return nil, err
	}
	resp, err := h.synaplanAPI.CloudWatchReconcileWithResponse(ctx, strconv.Itoa(watchID), payload)
	if err != nil {
		return nil, fmt.Errorf("reconcile: %w", err)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("reconcile: %w", upstream(resp.StatusCode(), resp.Body))
	}
	var decision struct {
		Process []string `json:"process"`
	}
	if err := json.Unmarshal(resp.Body, &decision); err != nil {
		return nil, fmt.Errorf("decoding the reconcile answer: %w", err)
	}
	wanted := make(map[string]bool, len(decision.Process))
	for _, id := range decision.Process {
		wanted[id] = true
	}
	return wanted, nil
}

// uploadWatchFile streams one file version to the watch. Synaplan decides
// whether it can read a file by the extension of its name, so the name is
// always sent.
func (h *Handler) uploadWatchFile(ctx context.Context, files watchFiles, watchID int, file cs3reader.Resource) error {
	opened, err := files.Open(ctx, file.ID)
	if err != nil {
		return err
	}
	defer func() { _ = opened.Body.Close() }()

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := writeWatchFile(mw, file, opened)
		if err == nil {
			err = mw.Close()
		}
		_ = pw.CloseWithError(err)
	}()
	resp, err := h.synaplanAPI.CloudWatchFileWithResponse(ctx, strconv.Itoa(watchID), withBody(mw.FormDataContentType(), pr))
	_ = pr.Close()
	if err != nil {
		return fmt.Errorf("upload %s: %w", file.Name, err)
	}
	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("upload %s: %w", file.Name, upstream(resp.StatusCode(), resp.Body))
	}
	return nil
}

// writeWatchFile writes the fields Synaplan's watch upload reads, and the
// bytes with the file's own content type: Synaplan reads the type from the
// part, and a bare CreateFormFile would say application/octet-stream.
// The version is the etag the reconcile named, so Synaplan files the bytes
// under the version it asked for.
func writeWatchFile(mw *multipart.Writer, file cs3reader.Resource, opened *cs3reader.File) error {
	for _, field := range [][2]string{{"id", file.ID}, {"version", file.Etag}, {"name", file.Name}, {"path", file.Path}} {
		if err := mw.WriteField(field[0], field[1]); err != nil {
			return err
		}
	}
	mimeType := opened.MimeType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, file.Name))
	header.Set("Content-Type", mimeType)
	part, err := mw.CreatePart(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(part, opened.Body)
	return err
}
