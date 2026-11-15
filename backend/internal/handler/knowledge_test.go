package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/synced"
)

// AddToKnowledge's validation runs before any CS3 or Synaplan call,
// so we can exercise it against a bare Handler value. Anything past
// validation is covered by E2E.

func TestAddToKnowledge_RejectsBadJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/synaplan/knowledge", strings.NewReader("{not json"))
	(&Handler{}).AddToKnowledge(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid JSON body") {
		t.Errorf("body = %q, want contains 'invalid JSON body'", rr.Body.String())
	}
}

func TestAddToKnowledge_RejectsBadInput(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{"no resourceId", `{"groupKey":"ABC"}`, "resourceId is required"},
		{"no groupKey", `{"resourceId":"r1"}`, "groupKey is required"},
		{"whitespace-only groupKey", `{"resourceId":"r1","groupKey":"   "}`, "groupKey is required"},
		{
			"oversized groupKey",
			`{"resourceId":"r1","groupKey":"` + strings.Repeat("x", knowledgeGroupMaxLen+1) + `"}`,
			"characters or fewer",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/synaplan/knowledge", strings.NewReader(tc.body))
			(&Handler{}).AddToKnowledge(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rr.Code)
			}
			if !strings.Contains(rr.Body.String(), tc.wantMsg) {
				t.Errorf("body = %q, want contains %q", rr.Body.String(), tc.wantMsg)
			}
		})
	}
}

func TestAddToKnowledge_AcceptsValidInput(t *testing.T) {
	// Past validation the handler hits CS3 and fails with 502 against
	// a bare Handler — anything non-400 proves validation passed.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/synaplan/knowledge",
		strings.NewReader(`{"resourceId":"r1","groupKey":"MY_GROUP"}`),
	)
	(&Handler{}).AddToKnowledge(rr, req)

	if rr.Code == http.StatusBadRequest {
		t.Errorf("validation rejected a valid request: %s", rr.Body.String())
	}
}

// knowledgeUpload records the knowledge upload and answers with chunks.
type knowledgeUpload struct {
	*httptest.Server
	status int
	chunks int

	mu      sync.Mutex
	uploads []string
}

func newKnowledgeUpload(t *testing.T) *knowledgeUpload {
	t.Helper()
	f := &knowledgeUpload{status: http.StatusOK, chunks: 1}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/files/upload", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		value := func(name string) string {
			got := r.MultipartForm.Value[name]
			if len(got) == 0 {
				return ""
			}
			return got[0]
		}
		file, _, err := r.FormFile("files[]")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		content, _ := io.ReadAll(file)
		row := strings.Join([]string{
			value("source_id"), value("source_etag"), value("group_key"),
			value("overwrite"), value("keep_synced"), string(content),
		}, "|")
		f.mu.Lock()
		f.uploads = append(f.uploads, row)
		status, chunks := f.status, f.chunks
		f.mu.Unlock()
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"files":   []map[string]any{{"id": 7, "chunks_created": chunks, "vectorized": chunks > 0}},
		})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *knowledgeUpload) handler(t *testing.T, store synced.Store) *Handler {
	t.Helper()
	h, err := New(nil, f.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.synced = store
	return h
}

func (f *knowledgeUpload) keepSynced(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.uploads) != 1 {
		t.Fatalf("uploads %v", f.uploads)
	}
	parts := strings.Split(f.uploads[0], "|")
	if len(parts) < 5 {
		t.Fatalf("upload %q", f.uploads[0])
	}
	return parts[4]
}

func textFile(etag string) *cs3reader.File {
	return &cs3reader.File{
		Name:     "a.txt",
		MimeType: "text/plain",
		Etag:     etag,
		Path:     "Inbox/a.txt",
		Body:     io.NopCloser(strings.NewReader("hello")),
	}
}

// rejectingStore refuses every write and lists nothing.
type rejectingStore struct{ err error }

func (s rejectingStore) Put(context.Context, synced.File) error { return s.err }
func (s rejectingStore) Update(context.Context, synced.File) (bool, error) {
	return false, s.err
}
func (s rejectingStore) Delete(context.Context, string, string, uint64) error { return nil }
func (s rejectingStore) Files(context.Context, string) ([]synced.File, error) {
	return nil, nil
}

// failSecondPut accepts the placeholder and refuses the confirmed etag.
type failSecondPut struct {
	*synced.MemoryStore
	puts int
}

func (s *failSecondPut) Put(ctx context.Context, file synced.File) error {
	s.puts++
	if s.puts >= 2 {
		return errors.New("store unavailable")
	}
	return s.MemoryStore.Put(ctx, file)
}

func TestKnowledgeDoesNotEnableSyncWhenTheStoreRefuses(t *testing.T) {
	syn := newKnowledgeUpload(t)
	store := rejectingStore{err: errors.New("store unavailable")}
	_, kept, err := syn.handler(t, store).uploadKnowledge(
		context.Background(), textFile("e1"), "s$sp!a", "DOCS", "ada", false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if kept {
		t.Fatal("reported the file as kept up to date")
	}
	if got := syn.keepSynced(t); got != "0" {
		t.Fatalf("keep_synced %q, want 0", got)
	}
}

func TestKnowledgeRecordsTheFileBeforeEnablingSync(t *testing.T) {
	syn := newKnowledgeUpload(t)
	store := synced.NewMemoryStore()
	_, kept, err := syn.handler(t, store).uploadKnowledge(
		context.Background(), textFile("e1"), "s$sp!a", "DOCS", "ada", false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !kept {
		t.Fatal("the file was not kept up to date")
	}
	if got := syn.keepSynced(t); got != "1" {
		t.Fatalf("keep_synced %q, want 1", got)
	}
	files, err := store.Files(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Etag != "e1" || files[0].GroupKey != "DOCS" {
		t.Fatalf("kept %+v, want e1 in DOCS", files)
	}
}

func TestKnowledgeKeepsAPlaceholderWhenTheConfirmedWriteFails(t *testing.T) {
	syn := newKnowledgeUpload(t)
	store := &failSecondPut{MemoryStore: synced.NewMemoryStore()}
	_, kept, err := syn.handler(t, store).uploadKnowledge(
		context.Background(), textFile("e1"), "s$sp!a", "DOCS", "ada", false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !kept {
		t.Fatal("the placeholder was dropped")
	}
	if got := syn.keepSynced(t); got != "1" {
		t.Fatalf("keep_synced %q, want 1", got)
	}
	files, err := store.Files(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Etag != "" {
		t.Fatalf("kept %+v, want an empty etag so the next check sends the file", files)
	}
}

func TestKnowledgeDropsAPlaceholderWhenNothingWasIndexed(t *testing.T) {
	syn := newKnowledgeUpload(t)
	syn.chunks = 0
	store := synced.NewMemoryStore()
	_, kept, err := syn.handler(t, store).uploadKnowledge(
		context.Background(), textFile("e1"), "s$sp!a", "DOCS", "ada", false,
	)
	if !errors.Is(err, errNoText) {
		t.Fatalf("error %v, want no text", err)
	}
	if kept {
		t.Fatal("a file that was not indexed was kept")
	}
	files, err := store.Files(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("kept %+v, want no row", files)
	}
}

func TestKnowledgeLeavesAnExistingRowWhenTheUploadFails(t *testing.T) {
	ctx := context.Background()
	syn := newKnowledgeUpload(t)
	syn.status = http.StatusInternalServerError
	store := synced.NewMemoryStore()
	if err := store.Put(ctx, synced.File{Subject: "ada", ResourceID: "s$sp!a", Etag: "e0", GroupKey: "DOCS"}); err != nil {
		t.Fatal(err)
	}
	_, kept, err := syn.handler(t, store).uploadKnowledge(ctx, textFile("e1"), "s$sp!a", "DOCS", "ada", true)
	if err == nil || kept {
		t.Fatalf("kept %v err %v, want the upload error and no new tracking", kept, err)
	}
	files, err := store.Files(ctx, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Etag != "e0" {
		t.Fatalf("kept %+v, want the previous etag e0", files)
	}
}
