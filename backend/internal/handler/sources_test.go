package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/synced"
)

// fakeSourcesSynaplan answers sources/events and the file upload, and
// records what it got.
type fakeSourcesSynaplan struct {
	*httptest.Server
	eventsStatus int
	// reupload is the ids Synaplan asks to upload again.
	reupload     []string
	uploadStatus map[string]int
	// chunks is chunks_created per source id. A missing id means 1.
	chunks map[string]int
	// onEvents runs after a batch is recorded, before the answer is written.
	onEvents func()
	// holdUpload, when set, blocks the first upload until it is closed.
	// heldUpload is closed once that upload is waiting.
	holdUpload chan struct{}
	heldUpload chan struct{}
	holdOnce   sync.Once

	mu      sync.Mutex
	batches [][]string
	uploads []string
}

func newFakeSourcesSynaplan(t *testing.T) *fakeSourcesSynaplan {
	t.Helper()
	f := &fakeSourcesSynaplan{eventsStatus: http.StatusOK, uploadStatus: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/platform-links/sources/events", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Events []sourceEvent `json:"events"`
		}
		if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		rows := make([]string, 0, len(body.Events))
		for _, e := range body.Events {
			rows = append(rows, e.Event+":"+e.ID+"@"+e.Version)
		}
		f.mu.Lock()
		f.batches = append(f.batches, rows)
		onEvents := f.onEvents
		f.mu.Unlock()
		if onEvents != nil {
			onEvents()
		}
		if f.eventsStatus != http.StatusOK {
			w.WriteHeader(f.eventsStatus)
			return
		}
		reupload := []map[string]string{}
		for _, id := range f.reupload {
			reupload = append(reupload, map[string]string{"external_id": "ext", "id": id})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"applied": len(body.Events), "ignored": 0, "reupload": reupload})
	})
	mux.HandleFunc("POST /api/v1/files/upload", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		field := func(name string) string { return r.MultipartForm.Value[name][0] }
		file, _, err := r.FormFile("files[]")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		content, _ := io.ReadAll(file)
		row := strings.Join([]string{field("source_id"), field("source_etag"), field("group_key"), field("overwrite"), field("keep_synced"), string(content)}, "|")
		f.mu.Lock()
		f.uploads = append(f.uploads, row)
		hold, held := f.holdUpload, f.heldUpload
		f.mu.Unlock()
		if hold != nil {
			f.holdOnce.Do(func() {
				close(held)
				<-hold
			})
		}
		if status := f.uploadStatus[field("source_id")]; status != 0 {
			w.WriteHeader(status)
			return
		}
		created := 1
		if n, ok := f.chunks[field("source_id")]; ok {
			created = n
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "files": []map[string]any{{"id": 1, "chunks_created": created}}})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeSourcesSynaplan) handler(t *testing.T, store synced.Store) *Handler {
	t.Helper()
	h, err := New(nil, f.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.synced = store
	return h
}

// keptInbox is ada's kept files: a.txt and b.pdf at the versions Synaplan
// last took, and a file that is gone from OpenCloud.
func keptInbox(t *testing.T) *synced.MemoryStore {
	t.Helper()
	store := synced.NewMemoryStore()
	for _, f := range []synced.File{
		{Subject: "ada", ResourceID: "s$sp!a", Etag: "ea", GroupKey: "DOCS"},
		{Subject: "ada", ResourceID: "s$sp!b", Etag: "old-b", GroupKey: "DOCS"},
		{Subject: "ada", ResourceID: "s$sp!gone", Etag: "eg", GroupKey: "DOCS"},
		{Subject: "eve", ResourceID: "s$sp!c", Etag: "old-c", GroupKey: "EVE"},
	} {
		if err := store.Put(context.Background(), f); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func TestKeepFilesCurrent(t *testing.T) {
	cases := []struct {
		name         string
		reupload     []string
		eventsStatus int
		failUpload   map[string]int
		chunks       map[string]int
		files        func() fakeFiles
		want         keptResult
		wantErr      bool
		wantBatches  [][]string
		wantUploads  []string
		// wantKept is ada's files afterwards, as id@version.
		wantKept []string
	}{
		{
			name:        "a changed file is uploaded again when Synaplan asks, and its version moves on",
			reupload:    []string{"s$sp!b"},
			files:       inbox,
			want:        keptResult{Checked: 3, Changed: 1, Deleted: 1, Sent: 1},
			wantBatches: [][]string{{"changed:s$sp!b@eb", "deleted:s$sp!gone@eg"}},
			wantUploads: []string{"s$sp!b|eb|DOCS|1|1|%PDF"},
			wantKept:    []string{"s$sp!a@ea", "s$sp!b@eb"},
		},
		{
			name:        "a change Synaplan wants no upload for still moves the version on",
			files:       inbox,
			want:        keptResult{Checked: 3, Changed: 1, Deleted: 1},
			wantBatches: [][]string{{"changed:s$sp!b@eb", "deleted:s$sp!gone@eg"}},
			wantKept:    []string{"s$sp!a@ea", "s$sp!b@eb"},
		},
		{
			name:        "an upload that indexes nothing keeps the old version pending",
			reupload:    []string{"s$sp!b"},
			chunks:      map[string]int{"s$sp!b": 0},
			files:       inbox,
			want:        keptResult{Checked: 3, Changed: 1, Deleted: 1, Failed: 1},
			wantBatches: [][]string{{"changed:s$sp!b@eb", "deleted:s$sp!gone@eg"}},
			wantUploads: []string{"s$sp!b|eb|DOCS|1|1|%PDF"},
			wantKept:    []string{"s$sp!a@ea", "s$sp!b@old-b"},
		},
		{
			name:     "a file that changed again is left for the next check",
			reupload: []string{"s$sp!b"},
			files: func() fakeFiles {
				f := inbox()
				f.openEtag = map[string]string{"s$sp!b": "eb-new"}
				return f
			},
			want:        keptResult{Checked: 3, Changed: 1, Deleted: 1},
			wantBatches: [][]string{{"changed:s$sp!b@eb", "deleted:s$sp!gone@eg"}},
			wantKept:    []string{"s$sp!a@ea", "s$sp!b@old-b"},
		},
		{
			name:        "a failed upload keeps the old version, so the next check reports it again",
			reupload:    []string{"s$sp!b"},
			failUpload:  map[string]int{"s$sp!b": http.StatusInternalServerError},
			files:       inbox,
			want:        keptResult{Checked: 3, Changed: 1, Deleted: 1, Failed: 1},
			wantBatches: [][]string{{"changed:s$sp!b@eb", "deleted:s$sp!gone@eg"}},
			wantUploads: []string{"s$sp!b|eb|DOCS|1|1|%PDF"},
			wantKept:    []string{"s$sp!a@ea", "s$sp!b@old-b"},
		},
		{
			name: "a gateway that fails is not a deletion",
			files: func() fakeFiles {
				f := inbox()
				f.broken["s$sp!gone"] = true
				f.broken["s$sp!b"] = true
				return f
			},
			want:     keptResult{Checked: 3},
			wantKept: []string{"s$sp!a@ea", "s$sp!b@old-b", "s$sp!gone@eg"},
		},
		{
			name:         "nothing moves when Synaplan does not take the events",
			eventsStatus: http.StatusForbidden,
			reupload:     []string{"s$sp!b"},
			files:        inbox,
			want:         keptResult{Checked: 3, Changed: 1, Deleted: 1},
			wantErr:      true,
			wantBatches:  [][]string{{"changed:s$sp!b@eb", "deleted:s$sp!gone@eg"}},
			wantKept:     []string{"s$sp!a@ea", "s$sp!b@old-b", "s$sp!gone@eg"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			syn := newFakeSourcesSynaplan(t)
			syn.reupload = tc.reupload
			if tc.eventsStatus != 0 {
				syn.eventsStatus = tc.eventsStatus
			}
			if tc.failUpload != nil {
				syn.uploadStatus = tc.failUpload
			}
			if tc.chunks != nil {
				syn.chunks = tc.chunks
			}
			store := keptInbox(t)
			got, err := syn.handler(t, store).keepFilesCurrent(context.Background(), tc.files(), "ada")
			if (err != nil) != tc.wantErr {
				t.Fatalf("error %v, want error %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("result %+v, want %+v", got, tc.want)
			}
			if fmt.Sprint(syn.batches) != fmt.Sprint(tc.wantBatches) {
				t.Fatalf("events %v, want %v", syn.batches, tc.wantBatches)
			}
			if !slices.Equal(syn.uploads, tc.wantUploads) {
				t.Fatalf("uploads %v, want %v", syn.uploads, tc.wantUploads)
			}
			files, err := store.Files(context.Background(), "ada")
			if err != nil {
				t.Fatal(err)
			}
			var kept []string
			for _, f := range files {
				kept = append(kept, f.ResourceID+"@"+f.Etag)
			}
			if !slices.Equal(kept, tc.wantKept) {
				t.Fatalf("kept %v, want %v", kept, tc.wantKept)
			}
			eve, _ := store.Files(context.Background(), "eve")
			if len(eve) != 1 || eve[0].Etag != "old-c" {
				t.Fatalf("another person's files changed: %+v", eve)
			}
		})
	}
}

func TestSourceEventsGoInBatchesSynaplanAccepts(t *testing.T) {
	files := fakeFiles{resources: map[string]cs3reader.Resource{}, broken: map[string]bool{}}
	store := synced.NewMemoryStore()
	for i := range sourceEventBatch + 1 {
		id := fmt.Sprintf("s$sp!%d", i)
		files.resources[id] = cs3reader.Resource{ID: id, Etag: "new"}
		if err := store.Put(context.Background(), synced.File{Subject: "ada", ResourceID: id, Etag: "old"}); err != nil {
			t.Fatal(err)
		}
	}
	syn := newFakeSourcesSynaplan(t)
	if _, err := syn.handler(t, store).keepFilesCurrent(context.Background(), files, "ada"); err != nil {
		t.Fatal(err)
	}
	if len(syn.batches) != 2 || len(syn.batches[0]) != sourceEventBatch || len(syn.batches[1]) != 1 {
		t.Fatalf("%d batches, want [%d 1]", len(syn.batches), sourceEventBatch)
	}
}

// changingFile reports a live etag for one file, so a pass can observe a
// newer version than the one an overlapping pass already opened.
type changingFile struct {
	fakeFiles
	id   string
	etag func() string
}

func (f changingFile) StatResource(ctx context.Context, id string) (cs3reader.Resource, error) {
	got, err := f.fakeFiles.StatResource(ctx, id)
	if err != nil || id != f.id {
		return got, err
	}
	got.Etag = f.etag()
	return got, nil
}

func (f changingFile) OpenVersion(ctx context.Context, id, etag string) (*cs3reader.File, error) {
	if id != f.id {
		return f.fakeFiles.OpenVersion(ctx, id, etag)
	}
	got := f.etag()
	if etag != "" && got != etag {
		return nil, cs3reader.ErrChanged
	}
	r := f.resources[id]
	return &cs3reader.File{
		Name:     r.Name,
		MimeType: r.MimeType,
		Etag:     got,
		Body:     io.NopCloser(strings.NewReader(f.content[id])),
	}, nil
}

func TestStalePassDoesNotUploadAfterANewerRevisionWins(t *testing.T) {
	ctx := context.Background()
	store := synced.NewMemoryStore()
	if err := store.Put(ctx, synced.File{Subject: "ada", ResourceID: "s$sp!b", Etag: "old-b", GroupKey: "DOCS"}); err != nil {
		t.Fatal(err)
	}
	syn := newFakeSourcesSynaplan(t)
	syn.reupload = []string{"s$sp!b"}
	syn.onEvents = func() {
		files, err := store.Files(ctx, "ada")
		if err != nil || len(files) != 1 {
			t.Errorf("kept files: %v %+v", err, files)
			return
		}
		file := files[0]
		file.Etag = "v-new"
		ok, err := store.Update(ctx, file)
		if err != nil || !ok {
			t.Errorf("recording the newer revision: ok=%v err=%v", ok, err)
		}
	}
	files := fakeFiles{
		resources: map[string]cs3reader.Resource{
			"s$sp!b": {ID: "s$sp!b", Name: "b.pdf", Etag: "eb", MimeType: "application/pdf"},
		},
		content: map[string]string{"s$sp!b": "%PDF"},
		broken:  map[string]bool{},
	}
	if _, err := syn.handler(t, store).keepFilesCurrent(ctx, files, "ada"); err != nil {
		t.Fatal(err)
	}
	syn.mu.Lock()
	uploads := slices.Clone(syn.uploads)
	syn.mu.Unlock()
	if len(uploads) != 0 {
		t.Fatalf("uploaded %v after a newer revision won", uploads)
	}
	kept, err := store.Files(ctx, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0].Etag != "v-new" {
		t.Fatalf("store %+v, want the newer revision v-new", kept)
	}
}

func TestOverlappingPassesDoNotUploadAnOlderCopyLast(t *testing.T) {
	ctx := context.Background()
	store := synced.NewMemoryStore()
	if err := store.Put(ctx, synced.File{Subject: "ada", ResourceID: "s$sp!b", Etag: "v1", GroupKey: "DOCS"}); err != nil {
		t.Fatal(err)
	}
	var current atomic.Value
	current.Store("v2")
	files := changingFile{
		fakeFiles: fakeFiles{
			resources: map[string]cs3reader.Resource{
				"s$sp!b": {ID: "s$sp!b", Name: "b.pdf", Etag: "v1", MimeType: "application/pdf"},
			},
			content: map[string]string{"s$sp!b": "%PDF"},
			broken:  map[string]bool{},
		},
		id:   "s$sp!b",
		etag: func() string { return current.Load().(string) },
	}
	syn := newFakeSourcesSynaplan(t)
	syn.reupload = []string{"s$sp!b"}
	hold := make(chan struct{})
	syn.holdUpload = hold
	syn.heldUpload = make(chan struct{})
	h := syn.handler(t, store)

	var wg sync.WaitGroup
	errc := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.keepFilesCurrent(ctx, files, "ada")
			errc <- err
		}()
	}
	select {
	case <-syn.heldUpload:
	case <-time.After(5 * time.Second):
		t.Fatal("the first upload did not start")
	}
	current.Store("v3")
	close(hold)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the passes did not finish")
	}
	close(errc)
	for err := range errc {
		if err != nil {
			t.Fatal(err)
		}
	}
	syn.mu.Lock()
	uploads := slices.Clone(syn.uploads)
	syn.mu.Unlock()
	if len(uploads) != 2 || !strings.Contains(uploads[0], "|v2|") || !strings.Contains(uploads[1], "|v3|") {
		t.Fatalf("uploads %v, want v2 and then v3", uploads)
	}
	kept, err := store.Files(ctx, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0].Etag != "v3" {
		t.Fatalf("store %+v, want v3, the version Synaplan received last", kept)
	}
}

func TestKnowledgeSyncNowNeedsOrganisationLogin(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Handler{}).KnowledgeSyncNow(rec, httptest.NewRequest(http.MethodPost, "/api/synaplan/knowledge/sync", nil))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "keep_unavailable") {
		t.Fatalf("answered %d %s", rec.Code, rec.Body.String())
	}
}
