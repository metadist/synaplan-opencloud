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
	"slices"
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
	// watchListTimeout bounds listing a watched target and asking Synaplan
	// what to upload, so a hung gateway or Synaplan fails fast.
	watchListTimeout = 30 * time.Second
	// watchUploadTimeout bounds each individual file upload, so a folder
	// with many files can keep progressing while one hung transfer fails.
	watchUploadTimeout = 2 * time.Minute
	// reconcileBatch is the most files Synaplan compares in one request.
	reconcileBatch = 500
	// maxWatchFileBytes matches Synaplan's WatchLimits::MAX_BYTES (100 MiB).
	maxWatchFileBytes = 100 << 20
)

// watchFiles is how a watch reaches the files it follows. The reva token
// on the context decides as whom: the signed-in person's own session, or
// machine auth for a person who is away.
type watchFiles interface {
	StatResource(ctx context.Context, resourceID string) (cs3reader.Resource, error)
	List(ctx context.Context, folderID string) ([]cs3reader.Resource, error)
	OpenVersion(ctx context.Context, resourceID, etag string) (*cs3reader.File, error)
}

var (
	// errFileChanged means the bytes are a different version from the one
	// Synaplan was asked about. The upload is skipped so the next check
	// sends the current version under its own etag.
	errFileChanged = errors.New("file changed since it was listed")
	// errWatchFileTooLarge means the file exceeds Synaplan's 100 MiB watch
	// upload cap, so it is not streamed over the wire.
	errWatchFileTooLarge = errors.New("file exceeds the 100 MiB watch limit")
)

var multipartQuoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"", "\r", "", "\n", "")

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
// It starts where the previous pass stopped, so slow watches at the front
// of the list cannot starve everyone after them.
func (h *Handler) reconcileEveryone(ctx context.Context) {
	subjects, err := h.delegation.Subjects(ctx)
	if err != nil {
		log.Printf("reconcile: listing people: %v", err)
		return
	}
	slices.Sort(subjects)
	start := h.cursorStart(&h.subjectCursor, len(subjects))
	ordered := rotate(subjects, start)
	processed := 0
	defer func() { h.cursorAdvance(&h.subjectCursor, start, processed, len(subjects)) }()
	for _, sub := range ordered {
		if ctx.Err() != nil {
			return
		}
		processed++
		away, err := h.actAs(ctx, sub)
		if errors.Is(err, delegation.ErrNotConnected) {
			h.dropWatchCursor(sub)
			continue
		}
		if err != nil {
			log.Printf("reconcile: acting for a person: %v", err)
			continue
		}
		if _, err := h.reconcileWatches(away, h.cs3, "", sub); err != nil {
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
		return nil, fmt.Errorf("minting a token for the person: %w", err)
	}
	value := delegation.Claim(token, h.userClaim)
	if value == "" {
		return nil, fmt.Errorf("the token names no %q claim", h.userClaim)
	}
	files, err := h.cs3.Impersonate(ctx, h.userAttr, value)
	if err != nil {
		return nil, fmt.Errorf("reading files as the person: %w", err)
	}
	return onBehalfOf(files, sub), nil
}

// WatchReconcileNow checks the signed-in person's watches on one folder or
// file now, with their own session, and sends Synaplan what it asks for.
func (h *Handler) WatchReconcileNow(w http.ResponseWriter, r *http.Request) {
	if h.refuseWatchSharedKey(w) {
		return
	}
	targetID, ok := requireTargetID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), passTimeout)
	defer cancel()
	result, err := h.reconcileWatches(ctx, h.cs3, targetID, "")
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
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
}

// reconcileWatches checks the active watches ctx's credential can see, or
// only those on targetID when it is set. A watch that fails is logged and
// counted; the others still run.
func (h *Handler) reconcileWatches(ctx context.Context, files watchFiles, targetID, subject string) (reconcileResult, error) {
	var result reconcileResult
	all, err := h.watches(ctx)
	if err != nil {
		return result, err
	}
	active := make([]watch, 0, len(all))
	for _, each := range all {
		if each.State != watchStateActive || (targetID != "" && each.TargetID != targetID) {
			continue
		}
		active = append(active, each)
	}
	slices.SortFunc(active, func(a, b watch) int { return a.ID - b.ID })
	start := 0
	if targetID == "" {
		start = h.watchStart(subject, len(active))
		active = rotate(active, start)
	}
	processed := 0
	if targetID == "" {
		defer func() { h.watchAdvance(subject, start, processed, len(active)) }()
	}
	for _, each := range active {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		processed++
		result.Watches++
		sent, skipped, failed, err := h.reconcileWatch(ctx, files, each)
		if err != nil {
			log.Printf("watch %d: %v", each.ID, err)
			failed++
		}
		result.Sent += sent
		result.Skipped += skipped
		result.Failed += failed
	}
	return result, nil
}

// reconcileWatch tells Synaplan which files the watch covers, at which
// version, and uploads the ones it asks for. Synaplan deduplicates each
// version on upload, so a pass that dies half way is finished by the next
// one and concurrent replicas never index a version twice.
func (h *Handler) reconcileWatch(ctx context.Context, files watchFiles, w watch) (sent, skipped, failed int, err error) {
	listCtx, cancelList := context.WithTimeout(ctx, watchListTimeout)
	covered, err := watchedFiles(listCtx, files, w)
	cancelList()
	if err != nil {
		return 0, 0, 0, err
	}
	for start := 0; start < len(covered); start += reconcileBatch {
		if ctx.Err() != nil {
			return sent, skipped, failed, ctx.Err()
		}
		batch := covered[start:min(start+reconcileBatch, len(covered))]
		askCtx, cancelAsk := context.WithTimeout(ctx, watchListTimeout)
		wanted, err := h.askReconcile(askCtx, w.ID, batch)
		cancelAsk()
		if err != nil {
			return sent, skipped, failed, err
		}
		for _, file := range batch {
			if !wanted[file.ID] {
				continue
			}
			if ctx.Err() != nil {
				return sent, skipped, failed, ctx.Err()
			}
			uploadCtx, cancelUpload := context.WithTimeout(ctx, watchUploadTimeout)
			state, err := h.uploadWatchFile(uploadCtx, files, w.ID, file)
			cancelUpload()
			if err != nil {
				if errors.Is(err, errFileChanged) {
					log.Printf("watch %d: %s changed; the next check sends the new version", w.ID, file.Name)
				} else {
					log.Printf("watch %d: upload: %v", w.ID, err)
				}
				failed++
				continue
			}
			switch state {
			case "skipped":
				skipped++
			case "already_handled":
			default:
				sent++
			}
		}
	}
	return sent, skipped, failed, nil
}

// watchedFiles is the watched file, or the files directly in the watched
// folder.
func watchedFiles(ctx context.Context, files watchFiles, w watch) ([]cs3reader.Resource, error) {
	if w.TargetKind == targetFile {
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

// uploadWatchFile streams one file version to the watch and returns the
// state Synaplan reported ("accepted", "skipped", or "already_handled").
// Synaplan decides whether it can read a file by the extension of its name,
// so the name is always sent.
func (h *Handler) uploadWatchFile(ctx context.Context, files watchFiles, watchID int, file cs3reader.Resource) (string, error) {
	if file.Size > maxWatchFileBytes {
		return "", fmt.Errorf("upload %s: %w", file.Name, errWatchFileTooLarge)
	}
	opened, err := files.OpenVersion(ctx, file.ID, file.Etag)
	if err != nil {
		if errors.Is(err, cs3reader.ErrChanged) {
			return "", errFileChanged
		}
		return "", fmt.Errorf("open %s: %w", file.Name, err)
	}
	defer func() { _ = opened.Body.Close() }()
	if opened.Etag != file.Etag {
		return "", errFileChanged
	}
	if opened.Size > maxWatchFileBytes {
		return "", fmt.Errorf("upload %s: %w", file.Name, errWatchFileTooLarge)
	}

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
		return "", fmt.Errorf("upload %s: %w", file.Name, err)
	}
	if resp.StatusCode() != http.StatusOK {
		return "", fmt.Errorf("upload %s: %w", file.Name, upstream(resp.StatusCode(), resp.Body))
	}
	var outcome struct {
		State string `json:"state"`
	}
	if json.Unmarshal(resp.Body, &outcome) == nil && outcome.State != "" {
		return outcome.State, nil
	}
	return "accepted", nil
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
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, multipartQuoteEscaper.Replace(file.Name)))
	header.Set("Content-Type", mimeType)
	part, err := mw.CreatePart(header)
	if err != nil {
		return err
	}
	n, err := io.Copy(part, io.LimitReader(opened.Body, maxWatchFileBytes+1))
	if err != nil {
		return err
	}
	if n > maxWatchFileBytes {
		return errWatchFileTooLarge
	}
	return nil
}

// watchStart is where subject's watch list continues. An empty subject
// is a check now, which does not move anyone's cursor.
func (h *Handler) watchStart(subject string, n int) int {
	if n == 0 || subject == "" {
		return 0
	}
	h.reconcileMu.Lock()
	defer h.reconcileMu.Unlock()
	if h.watchCursors == nil {
		h.watchCursors = map[string]int{}
	}
	cursor := h.watchCursors[subject]
	if cursor < 0 {
		cursor = 0
	}
	return cursor % n
}

// watchAdvance records how far subject's list got. Another person's list
// has its own entry, so finishing their one watch cannot send this person
// back to the start.
func (h *Handler) watchAdvance(subject string, start, processed, n int) {
	if n == 0 || processed <= 0 || subject == "" {
		return
	}
	if processed > n {
		processed = n
	}
	h.reconcileMu.Lock()
	if h.watchCursors == nil {
		h.watchCursors = map[string]int{}
	}
	h.watchCursors[subject] = (start + processed) % n
	h.reconcileMu.Unlock()
}

func (h *Handler) dropWatchCursor(subject string) {
	if subject == "" {
		return
	}
	h.reconcileMu.Lock()
	delete(h.watchCursors, subject)
	h.reconcileMu.Unlock()
}

// cursorStart is the index this pass begins at. n is how many items there
// are now; the stored cursor may be from a longer list.
func (h *Handler) cursorStart(cursor *int, n int) int {
	if n == 0 {
		return 0
	}
	h.reconcileMu.Lock()
	defer h.reconcileMu.Unlock()
	if *cursor < 0 {
		*cursor = 0
	}
	return *cursor % n
}

// cursorAdvance moves the cursor past the items this pass started. A pass
// that started none leaves the cursor where it is, so a deadline that
// expires before any work does not skip someone.
func (h *Handler) cursorAdvance(cursor *int, start, processed, n int) {
	if n == 0 || processed <= 0 {
		return
	}
	if processed > n {
		processed = n
	}
	h.reconcileMu.Lock()
	*cursor = (start + processed) % n
	h.reconcileMu.Unlock()
}

// rotate returns items beginning at start, then the ones before it.
func rotate[T any](items []T, start int) []T {
	if len(items) == 0 {
		return items
	}
	start %= len(items)
	if start == 0 {
		return items
	}
	out := make([]T, 0, len(items))
	out = append(out, items[start:]...)
	out = append(out, items[:start]...)
	return out
}
