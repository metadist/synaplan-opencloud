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
)

// fakeWatchSynaplan answers the cloud watch routes and records what it got.
type fakeWatchSynaplan struct {
	*httptest.Server
	watches    []watch
	listStatus int
	listBody   string
	// process is what Synaplan asks to upload, per watch.
	process      map[int][]string
	uploadStatus map[string]int
	stopStatus   map[int]int

	mu         sync.Mutex
	reconciled map[int][][]string
	uploads    []upload
	stops      []string
}

type upload struct {
	watch       string
	fields      map[string]string
	content     string
	contentType string
}

func newFakeSynaplan(t *testing.T, watches []watch) *fakeWatchSynaplan {
	t.Helper()
	f := &fakeWatchSynaplan{
		watches:      watches,
		listStatus:   http.StatusOK,
		process:      map[int][]string{},
		uploadStatus: map[string]int{},
		stopStatus:   map[int]int{},
		reconciled:   map[int][][]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/cloud/watches", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(f.listStatus)
		if f.listBody != "" {
			_, _ = io.WriteString(w, f.listBody)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": f.watches, "paused_count": 0})
	})
	mux.HandleFunc("POST /api/v1/cloud/watches/{id}/reconcile", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Items []struct {
				ID      string `json:"id"`
				Version string `json:"version"`
			} `json:"items"`
		}
		if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var id int
		_, _ = fmt.Sscan(r.PathValue("id"), &id)
		rows := make([]string, 0, len(body.Items))
		for _, item := range body.Items {
			rows = append(rows, item.ID+"@"+item.Version)
		}
		f.mu.Lock()
		f.reconciled[id] = append(f.reconciled[id], rows)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"process": f.process[id], "forget": []string{}})
	})
	mux.HandleFunc("POST /api/v1/cloud/watches/{id}/files", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		got := upload{watch: r.PathValue("id"), fields: map[string]string{}}
		for key, values := range r.MultipartForm.Value {
			got.fields[key] = values[0]
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		content, _ := io.ReadAll(file)
		got.content = string(content)
		got.contentType = header.Header.Get("Content-Type")
		f.mu.Lock()
		f.uploads = append(f.uploads, got)
		f.mu.Unlock()
		if status := f.uploadStatus[got.fields["id"]]; status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":"invalid_watch","message":"This watch is not running."}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"state": "accepted"})
	})
	mux.HandleFunc("DELETE /api/v1/cloud/watches/{id}", func(w http.ResponseWriter, r *http.Request) {
		var id int
		_, _ = fmt.Sscan(r.PathValue("id"), &id)
		f.mu.Lock()
		f.stops = append(f.stops, r.PathValue("id"))
		f.mu.Unlock()
		if status := f.stopStatus[id]; status != 0 {
			w.WriteHeader(status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "state": "stopped"})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeWatchSynaplan) handler(t *testing.T) *Handler {
	t.Helper()
	h, err := New(nil, f.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (f *fakeWatchSynaplan) stopped() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.stops)
}

// fakeFiles is a person's files: folders by id with their children, and
// the bytes of each file.
type fakeFiles struct {
	resources map[string]cs3reader.Resource
	children  map[string][]string
	content   map[string]string
	broken    map[string]bool
}

func (f fakeFiles) StatResource(_ context.Context, id string) (cs3reader.Resource, error) {
	if f.broken[id] {
		return cs3reader.Resource{}, errors.New("gateway down")
	}
	r, ok := f.resources[id]
	if !ok {
		return cs3reader.Resource{}, cs3reader.ErrNotFound
	}
	return r, nil
}

func (f fakeFiles) List(_ context.Context, id string) ([]cs3reader.Resource, error) {
	if f.broken[id] {
		return nil, errors.New("gateway down")
	}
	var out []cs3reader.Resource
	for _, child := range f.children[id] {
		out = append(out, f.resources[child])
	}
	return out, nil
}

func (f fakeFiles) Open(_ context.Context, id string) (*cs3reader.File, error) {
	r := f.resources[id]
	if f.broken[id] {
		return nil, errors.New("gateway down")
	}
	return &cs3reader.File{Name: r.Name, MimeType: r.MimeType, Etag: r.Etag, Body: io.NopCloser(strings.NewReader(f.content[id]))}, nil
}

func inbox() fakeFiles {
	return fakeFiles{
		resources: map[string]cs3reader.Resource{
			"s$sp!inbox": {ID: "s$sp!inbox", Name: "Inbox", Path: "Inbox", IsFolder: true},
			"s$sp!a":     {ID: "s$sp!a", Name: "a.txt", Path: "Inbox/a.txt", Etag: "ea", MimeType: "text/plain"},
			"s$sp!b":     {ID: "s$sp!b", Name: "b.pdf", Path: "Inbox/b.pdf", Etag: "eb", MimeType: "application/pdf"},
			"s$sp!sub":   {ID: "s$sp!sub", Name: "Archive", Path: "Inbox/Archive", IsFolder: true},
			"s$sp!c":     {ID: "s$sp!c", Name: "c.md", Path: "c.md", Etag: "ec"},
		},
		children: map[string][]string{"s$sp!inbox": {"s$sp!a", "s$sp!sub", "s$sp!b"}},
		content:  map[string]string{"s$sp!a": "alpha", "s$sp!b": "%PDF", "s$sp!c": "# c"},
		broken:   map[string]bool{},
	}
}

func TestReconcileWatches(t *testing.T) {
	folderWatch := watch{ID: 7, TargetKind: "folder", TargetID: "s$sp!inbox", State: "active"}
	fileWatch := watch{ID: 8, TargetKind: "file", TargetID: "s$sp!c", State: "active"}
	cases := []struct {
		name       string
		watches    []watch
		process    map[int][]string
		failUpload map[string]int
		files      func() fakeFiles
		target     string
		want       reconcileResult
		// wantAsked is what each watch told Synaplan it covers.
		wantAsked map[int][][]string
		// wantSent is the files uploaded, as watch/id/version/name/path/content/type.
		wantSent []string
	}{
		{
			name:      "a folder sends only the files Synaplan asks for, never a subfolder",
			watches:   []watch{folderWatch},
			process:   map[int][]string{7: {"s$sp!b"}},
			files:     inbox,
			want:      reconcileResult{Watches: 1, Sent: 1},
			wantAsked: map[int][][]string{7: {{"s$sp!a@ea", "s$sp!b@eb"}}},
			wantSent:  []string{"7|s$sp!b|eb|b.pdf|Inbox/b.pdf|%PDF|application/pdf"},
		},
		{
			name:      "a file Synaplan already has is not sent again",
			watches:   []watch{fileWatch},
			files:     inbox,
			want:      reconcileResult{Watches: 1},
			wantAsked: map[int][][]string{8: {{"s$sp!c@ec"}}},
		},
		{
			name:      "a file without a type goes as octet-stream",
			watches:   []watch{fileWatch},
			process:   map[int][]string{8: {"s$sp!c"}},
			files:     inbox,
			want:      reconcileResult{Watches: 1, Sent: 1},
			wantAsked: map[int][][]string{8: {{"s$sp!c@ec"}}},
			wantSent:  []string{"8|s$sp!c|ec|c.md|c.md|# c|application/octet-stream"},
		},
		{
			name:      "paused and stopped watches are left alone",
			watches:   []watch{{ID: 9, TargetKind: "folder", TargetID: "s$sp!inbox", State: "paused"}, {ID: 10, TargetKind: "file", TargetID: "s$sp!c", State: "attention"}},
			files:     inbox,
			want:      reconcileResult{},
			wantAsked: map[int][][]string{},
		},
		{
			name:      "check now covers only the item it was asked for",
			watches:   []watch{folderWatch, fileWatch},
			process:   map[int][]string{7: {"s$sp!a"}, 8: {"s$sp!c"}},
			files:     inbox,
			target:    "s$sp!c",
			want:      reconcileResult{Watches: 1, Sent: 1},
			wantAsked: map[int][][]string{8: {{"s$sp!c@ec"}}},
			wantSent:  []string{"8|s$sp!c|ec|c.md|c.md|# c|application/octet-stream"},
		},
		{
			name:       "a refused upload is counted and the next file still goes",
			watches:    []watch{folderWatch},
			process:    map[int][]string{7: {"s$sp!a", "s$sp!b"}},
			failUpload: map[string]int{"s$sp!a": http.StatusConflict},
			files:      inbox,
			want:       reconcileResult{Watches: 1, Sent: 1, Failed: 1},
			wantAsked:  map[int][][]string{7: {{"s$sp!a@ea", "s$sp!b@eb"}}},
			wantSent: []string{
				"7|s$sp!a|ea|a.txt|Inbox/a.txt|alpha|text/plain",
				"7|s$sp!b|eb|b.pdf|Inbox/b.pdf|%PDF|application/pdf",
			},
		},
		{
			name:    "a folder that cannot be read fails that watch, not the others",
			watches: []watch{folderWatch, fileWatch},
			process: map[int][]string{8: {"s$sp!c"}},
			files: func() fakeFiles {
				f := inbox()
				f.broken["s$sp!inbox"] = true
				return f
			},
			want:      reconcileResult{Watches: 2, Sent: 1, Failed: 1},
			wantAsked: map[int][][]string{8: {{"s$sp!c@ec"}}},
			wantSent:  []string{"8|s$sp!c|ec|c.md|c.md|# c|application/octet-stream"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			syn := newFakeSynaplan(t, tc.watches)
			if tc.process != nil {
				syn.process = tc.process
			}
			if tc.failUpload != nil {
				syn.uploadStatus = tc.failUpload
			}
			got, err := syn.handler(t).reconcileWatches(context.Background(), tc.files(), tc.target)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("result %+v, want %+v", got, tc.want)
			}
			if fmt.Sprint(syn.reconciled) != fmt.Sprint(tc.wantAsked) {
				t.Fatalf("asked Synaplan about %v, want %v", syn.reconciled, tc.wantAsked)
			}
			var sent []string
			for _, u := range syn.uploads {
				sent = append(sent, strings.Join([]string{u.watch, u.fields["id"], u.fields["version"], u.fields["name"], u.fields["path"], u.content, u.contentType}, "|"))
			}
			if !slices.Equal(sent, tc.wantSent) {
				t.Fatalf("sent %v, want %v", sent, tc.wantSent)
			}
		})
	}
}

func TestReconcileAsksInBatchesSynaplanAccepts(t *testing.T) {
	files := fakeFiles{
		resources: map[string]cs3reader.Resource{},
		children:  map[string][]string{},
		content:   map[string]string{},
	}
	for i := range reconcileBatch + 1 {
		id := fmt.Sprintf("s$sp!%d", i)
		files.resources[id] = cs3reader.Resource{ID: id, Name: id + ".txt", Etag: "e"}
		files.children["s$sp!big"] = append(files.children["s$sp!big"], id)
	}
	syn := newFakeSynaplan(t, []watch{{ID: 1, TargetKind: "folder", TargetID: "s$sp!big", State: "active"}})

	if _, err := syn.handler(t).reconcileWatches(context.Background(), files, ""); err != nil {
		t.Fatal(err)
	}
	batches := syn.reconciled[1]
	if len(batches) != 2 || len(batches[0]) != reconcileBatch || len(batches[1]) != 1 {
		sizes := make([]int, 0, len(batches))
		for _, b := range batches {
			sizes = append(sizes, len(b))
		}
		t.Fatalf("batches of %v, want [%d 1]", sizes, reconcileBatch)
	}
}

func TestRunReconcilerNeedsDelegationAndMachineAuth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		(&Handler{}).RunReconciler(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("the reconciler ran without organisation login and machine auth")
	}
}
