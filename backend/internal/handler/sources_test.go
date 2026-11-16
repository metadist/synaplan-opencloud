package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

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
	uploadChunks map[string]int

	mu      sync.Mutex
	batches [][]string
	uploads []string
}

func newFakeSourcesSynaplan(t *testing.T) *fakeSourcesSynaplan {
	t.Helper()
	f := &fakeSourcesSynaplan{
		eventsStatus: http.StatusOK,
		uploadStatus: map[string]int{},
		uploadChunks: map[string]int{},
	}
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
		f.mu.Unlock()
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
		field := func(name string) string {
			vals := r.MultipartForm.Value[name]
			if len(vals) == 0 {
				return "<unset>"
			}
			return vals[0]
		}
		file, _, err := r.FormFile("files[]")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		content, _ := io.ReadAll(file)
		row := strings.Join([]string{field("source_id"), field("source_etag"), field("group_key"), field("overwrite"), field("keep_synced"), string(content)}, "|")
		f.mu.Lock()
		f.uploads = append(f.uploads, row)
		f.mu.Unlock()
		if status := f.uploadStatus[field("source_id")]; status != 0 {
			w.WriteHeader(status)
			return
		}
		chunks := 1
		if custom, ok := f.uploadChunks[field("source_id")]; ok {
			chunks = custom
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "files": []map[string]any{{"id": 1, "chunks_created": chunks}}})
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
		uploadChunks map[string]int
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
			name:         "an upload that indexes zero chunks fails and keeps the old version",
			reupload:     []string{"s$sp!b"},
			uploadChunks: map[string]int{"s$sp!b": 0},
			files:        inbox,
			want:         keptResult{Checked: 3, Changed: 1, Deleted: 1, Failed: 1},
			wantBatches:  [][]string{{"changed:s$sp!b@eb", "deleted:s$sp!gone@eg"}},
			wantUploads:  []string{"s$sp!b|eb|DOCS|1|1|%PDF"},
			wantKept:     []string{"s$sp!a@ea", "s$sp!b@old-b"},
		},
		{
			name:     "a file that changed again between scan and open is deferred without uploading",
			reupload: []string{"s$sp!b"},
			files: func() fakeFiles {
				f := inbox()
				f.openEtag = map[string]string{"s$sp!b": "eb-newer"}
				return f
			},
			want:        keptResult{Checked: 3, Changed: 1, Deleted: 1},
			wantBatches: [][]string{{"changed:s$sp!b@eb", "deleted:s$sp!gone@eg"}},
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
			if tc.uploadChunks != nil {
				syn.uploadChunks = tc.uploadChunks
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

func TestStaleSourceChangeDoesNotOverwriteNewerRevision(t *testing.T) {
	syn := newFakeSourcesSynaplan(t)
	syn.reupload = []string{"s$sp!b"}
	store := synced.NewMemoryStore()
	if err := store.Put(context.Background(), synced.File{Subject: "ada", ResourceID: "s$sp!b", Etag: "e1", GroupKey: "DOCS"}); err != nil {
		t.Fatal(err)
	}
	stale, found, err := store.Get(context.Background(), "ada", "s$sp!b")
	if err != nil || !found {
		t.Fatalf("initial get: %v %v", found, err)
	}
	// Simulate another replica or manual AddToKnowledge moving the entry to e3.
	newer := stale
	newer.Etag = "e3"
	if _, err := store.Update(context.Background(), newer); err != nil {
		t.Fatal(err)
	}

	files := inbox()
	r := files.resources["s$sp!b"]
	r.Etag = "e2"
	files.resources["s$sp!b"] = r

	sent, failed, err := syn.handler(t, store).sendSourceEvents(context.Background(), files, []sourceChange{{
		kept:  stale,
		event: sourceEvent{ID: "s$sp!b", Event: "changed", Version: "e2"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if sent != 0 || failed != 0 {
		t.Fatalf("sent=%d failed=%d, want 0/0", sent, failed)
	}
	if len(syn.uploads) != 0 {
		t.Fatalf("stale pass uploaded %v over newer revision", syn.uploads)
	}
	got, _, _ := store.Get(context.Background(), "ada", "s$sp!b")
	if got.Etag != "e3" {
		t.Fatalf("stored etag %q, want e3", got.Etag)
	}
}

// brokenSyncedStore simulates a store failure on Get or Create.
type brokenSyncedStore struct {
	*synced.MemoryStore
	failGet    bool
	failCreate bool
}

func (b *brokenSyncedStore) Get(ctx context.Context, sub, id string) (synced.File, bool, error) {
	if b.failGet {
		return synced.File{}, false, errors.New("store unreachable")
	}
	return b.MemoryStore.Get(ctx, sub, id)
}

func (b *brokenSyncedStore) Create(ctx context.Context, f synced.File) (uint64, bool, error) {
	if b.failCreate {
		return 0, false, errors.New("store write refused")
	}
	return b.MemoryStore.Create(ctx, f)
}

func TestUploadKnowledgeTracksAtomically(t *testing.T) {
	sample := func() *cs3reader.File {
		return &cs3reader.File{
			Name:     "notes.txt",
			MimeType: "text/plain",
			Etag:     "etag-1",
			Body:     io.NopCloser(strings.NewReader("hello")),
		}
	}

	t.Run("records version when upload succeeds", func(t *testing.T) {
		syn := newFakeSourcesSynaplan(t)
		store := synced.NewMemoryStore()
		_, kept, blocked, err := syn.handler(t, store).uploadKnowledge(context.Background(), sample(), "s$sp!n", "DOCS", "ada", true)
		if err != nil || !kept || blocked != "" {
			t.Fatalf("got kept=%v blocked=%q err=%v", kept, blocked, err)
		}
		row, found, _ := store.Get(context.Background(), "ada", "s$sp!n")
		if !found || row.Etag != "etag-1" || row.GroupKey != "DOCS" {
			t.Fatalf("stored %+v found=%v", row, found)
		}
		if !slices.Equal(syn.uploads, []string{"s$sp!n|etag-1|DOCS|1|1|hello"}) {
			t.Fatalf("uploads %v", syn.uploads)
		}
	})

	t.Run("removes placeholder when upload fails", func(t *testing.T) {
		syn := newFakeSourcesSynaplan(t)
		syn.uploadStatus["s$sp!n"] = http.StatusBadGateway
		store := synced.NewMemoryStore()
		_, _, _, err := syn.handler(t, store).uploadKnowledge(context.Background(), sample(), "s$sp!n", "DOCS", "ada", false)
		if err == nil {
			t.Fatal("expected upload error")
		}
		if _, found, _ := store.Get(context.Background(), "ada", "s$sp!n"); found {
			t.Fatal("failed upload left an orphaned placeholder in the store")
		}
	})

	t.Run("sends keep_synced=0 when placeholder creation fails", func(t *testing.T) {
		syn := newFakeSourcesSynaplan(t)
		store := &brokenSyncedStore{MemoryStore: synced.NewMemoryStore(), failCreate: true}
		_, kept, blocked, err := syn.handler(t, store).uploadKnowledge(context.Background(), sample(), "s$sp!n", "DOCS", "ada", false)
		if err != nil || kept || blocked != "store" {
			t.Fatalf("got kept=%v blocked=%q err=%v, want false/store/nil", kept, blocked, err)
		}
		if !slices.Equal(syn.uploads, []string{"s$sp!n|etag-1|DOCS|<unset>|0|hello"}) {
			t.Fatalf("uploads %v, want keep_synced=0", syn.uploads)
		}
	})

	t.Run("leaves keep_synced unset when reading existing state fails", func(t *testing.T) {
		syn := newFakeSourcesSynaplan(t)
		store := &brokenSyncedStore{MemoryStore: synced.NewMemoryStore(), failGet: true}
		_, kept, blocked, err := syn.handler(t, store).uploadKnowledge(context.Background(), sample(), "s$sp!n", "DOCS", "ada", true)
		if err != nil || kept || blocked != "store" {
			t.Fatalf("got kept=%v blocked=%q err=%v, want false/store/nil", kept, blocked, err)
		}
		if !slices.Equal(syn.uploads, []string{"s$sp!n|etag-1|DOCS|1|<unset>|hello"}) {
			t.Fatalf("uploads %v, want keep_synced unset", syn.uploads)
		}
	})
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

func TestKnowledgeSyncNowNeedsOrganisationLogin(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Handler{}).KnowledgeSyncNow(rec, httptest.NewRequest(http.MethodPost, "/api/synaplan/knowledge/sync", nil))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "keep_unavailable") {
		t.Fatalf("answered %d %s", rec.Code, rec.Body.String())
	}
}
