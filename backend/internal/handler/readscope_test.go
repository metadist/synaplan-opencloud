package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
)

func TestReadScopeRoutesRejectBadInput(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{"asking without a folder", http.MethodGet, "/api/synaplan/read-scope", ""},
		{"asking with a blank folder", http.MethodGet, "/api/synaplan/read-scope?folderId=%20", ""},
		{"changing with no JSON", http.MethodPut, "/api/synaplan/read-scope", "nope"},
		{"changing without a folder", http.MethodPut, "/api/synaplan/read-scope", `{"allow":true}`},
		{"changing with a blank folder", http.MethodPut, "/api/synaplan/read-scope", `{"folderId":"  ","allow":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			if tc.method == http.MethodGet {
				h.ReadScope(rec, req)
			} else {
				h.SetReadScope(rec, req)
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d %s, want 400", rec.Code, rec.Body)
			}
		})
	}
}

func TestWithFolder(t *testing.T) {
	hr := cs3reader.Resource{ID: "s$sp!hr", Path: "HR", IsFolder: true}
	specs := scopeFolder{Path: "Specs"}
	cases := []struct {
		name    string
		scope   readScope
		allow   bool
		want    readScope
		wantErr error
	}{
		{
			name:  "allowing the first folder names it",
			scope: readScope{Mode: scopeWatched, Folders: []scopeFolder{}},
			allow: true,
			want:  readScope{Mode: scopeAllowed, Folders: []scopeFolder{{ID: hr.ID, Path: "HR"}}},
		},
		{
			name:  "allowing keeps the folders named before",
			scope: readScope{Mode: scopeAllowed, Folders: []scopeFolder{specs}},
			allow: true,
			want:  readScope{Mode: scopeAllowed, Folders: []scopeFolder{specs, {ID: hr.ID, Path: "HR"}}},
		},
		{
			name:  "allowing a named folder again names it once",
			scope: readScope{Mode: scopeAllowed, Folders: []scopeFolder{{Path: "HR"}, specs}},
			allow: true,
			want:  readScope{Mode: scopeAllowed, Folders: []scopeFolder{specs, {ID: hr.ID, Path: "HR"}}},
		},
		{
			name:  "stopping keeps the other folders",
			scope: readScope{Mode: scopeAllowed, Folders: []scopeFolder{{Path: "HR"}, specs}},
			want:  readScope{Mode: scopeAllowed, Folders: []scopeFolder{specs}},
		},
		{
			name:  "stopping the last folder falls back to the watched ones",
			scope: readScope{Mode: scopeAllowed, Folders: []scopeFolder{{Path: "HR"}}},
			want:  readScope{Mode: scopeWatched, Folders: []scopeFolder{}},
		},
		{
			name:  "allowing while everything is allowed changes nothing",
			scope: readScope{Mode: scopeAll, Folders: []scopeFolder{}},
			allow: true,
			want:  readScope{Mode: scopeAll, Folders: []scopeFolder{}},
		},
		{
			name:    "one folder cannot be left out of everything",
			scope:   readScope{Mode: scopeAll, Folders: []scopeFolder{}},
			wantErr: errReadsEverything,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := withFolder(tc.scope, hr, "s$sp", tc.allow)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestWithFolderDoesNotTouchAnotherSpace(t *testing.T) {
	personal := "s$mine"
	hr := cs3reader.Resource{ID: "s$mine!hr", Path: "HR", IsFolder: true}
	other := cs3reader.Resource{ID: "s$theirs!hr", Path: "HR", IsFolder: true}
	start := readScope{Mode: scopeAllowed, Folders: []scopeFolder{{ID: hr.ID, Path: "HR"}}}
	got, err := withFolder(start, other, personal, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, start) {
		t.Fatalf("stopping the other space removed the personal grant: %+v", got)
	}
	if _, err := withFolder(start, other, personal, true); !errors.Is(err, errOutsidePersonal) {
		t.Fatalf("allow err %v, want outside the personal space", err)
	}
}

func TestLinkedReadScope(t *testing.T) {
	link := func(client, sub, status, scope string) string {
		return fmt.Sprintf(`{"client":%q,"external_id":%q,"status":%q,"read_scope":%s}`, client, sub, status, scope)
	}
	named := `{"mode":"allowed","folders":["/HR","Specs"]}`
	cases := []struct {
		name  string
		links []string
		want  readScope
	}{
		{
			name:  "the person's link to an OpenCloud",
			links: []string{link("nextcloud", "ada", "active", `{"mode":"all","folders":[]}`), link("opencloud", "ada", "active", named)},
			want:  readScope{Mode: scopeAllowed, Folders: []scopeFolder{{Path: "HR"}, {Path: "Specs"}}},
		},
		{
			name:  "another person's link is not theirs",
			links: []string{link("opencloud", "eve", "active", named)},
			want:  readScope{Mode: scopeWatched, Folders: []scopeFolder{}},
		},
		{
			name:  "a link that is off reads nothing",
			links: []string{link("opencloud", "ada", "off", named)},
			want:  readScope{Mode: scopeWatched, Folders: []scopeFolder{}},
		},
		{
			name: "no link yet is the default",
			want: readScope{Mode: scopeWatched, Folders: []scopeFolder{}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"success":true,"links":[` + strings.Join(tc.links, ",") + `]}`
			got, err := linkedReadScope([]byte(body), "ada")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestReadScopeFailure(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"no organisation login", errNoDelegation, http.StatusConflict, "reads_unavailable"},
		{"everything is allowed", errReadsEverything, http.StatusConflict, "reads_everything"},
		{"a folder outside the personal space", errOutsidePersonal, http.StatusConflict, "outside_personal"},
		{"Synaplan does not know the person here", upstream(http.StatusNotFound, []byte(`{"error":"unknown_identity"}`)), http.StatusConflict, "cloud_not_linked"},
		{"Synaplan does not know this cloud", upstream(http.StatusForbidden, []byte(`{"error":"platform_disconnected"}`)), http.StatusConflict, "cloud_not_linked"},
		{"Synaplan fails", upstream(http.StatusInternalServerError, nil), http.StatusBadGateway, "synaplan_error"},
		{"Synaplan cannot be reached", errors.New("dial tcp: refused"), http.StatusBadGateway, "synaplan_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := readScopeFailure(tc.err)
			if status != tc.status || body.Code != tc.code || body.Error == "" {
				t.Fatalf("got %d %+v, want %d %s", status, body, tc.status, tc.code)
			}
		})
	}
}

func TestCloudTokenRemembersTheScopeItCannotList(t *testing.T) {
	var mu sync.Mutex
	var saved []readScope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/me/platform-links":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"success":false,"error":"insufficient_scope","code":"insufficient_scope","message":"no"}`))
		case "PUT /api/v1/platform-links/read-scope":
			var body struct {
				Mode    string        `json:"mode"`
				Folders []scopeFolder `json:"folders"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			saved = append(saved, readScope{Mode: body.Mode, Folders: body.Folders})
			mu.Unlock()
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	h, err := New(nil, srv.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	hr := cs3reader.Resource{ID: "s$sp!hr", Path: "HR", IsFolder: true}
	specs := cs3reader.Resource{ID: "s$sp!specs", Path: "Specs", IsFolder: true}
	if _, err := h.commitReadScope(context.Background(), "ada", "s$sp", hr, false); err != nil {
		t.Fatal(err)
	}
	if _, err := h.commitReadScope(context.Background(), "ada", "s$sp", hr, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.commitReadScope(context.Background(), "ada", "s$sp", specs, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.commitReadScope(context.Background(), "ada", "s$sp", hr, false); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(saved) != 4 {
		t.Fatalf("reported %d scopes, want 4", len(saved))
	}
	paths := func(scope readScope) []string {
		out := make([]string, 0, len(scope.Folders))
		for _, folder := range scope.Folders {
			out = append(out, folder.Path)
		}
		return out
	}
	if got := paths(saved[1]); !reflect.DeepEqual(got, []string{"HR"}) {
		t.Fatalf("first allow reported %v", got)
	}
	if got := paths(saved[2]); !reflect.DeepEqual(got, []string{"HR", "Specs"}) {
		t.Fatalf("second allow dropped a folder: %v", got)
	}
	if got := paths(saved[3]); !reflect.DeepEqual(got, []string{"Specs"}) {
		t.Fatalf("stopping HR reported %v", got)
	}
}

// blockingBook is a scope Synaplan would keep. The first save waits until
// the test releases it, so a second update can try to overlap.
type blockingBook struct {
	mu         sync.Mutex
	published  readScope
	saves      int
	release    chan struct{}
	firstSaved chan struct{}
}

func (b *blockingBook) Load(context.Context, string, *scopeOp) (readScope, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.published, nil
}

func (b *blockingBook) Save(_ context.Context, _ string, next readScope) error {
	b.mu.Lock()
	n := b.saves
	b.saves++
	b.mu.Unlock()
	if n == 0 {
		close(b.firstSaved)
		<-b.release
	}
	b.mu.Lock()
	b.published = next
	b.mu.Unlock()
	return nil
}

func TestOverlappingReadScopeUpdatesDoNotRestoreARemovedFolder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		book := &blockingBook{
			published:  readScope{Mode: scopeAllowed, Folders: []scopeFolder{{Path: "HR"}}},
			release:    make(chan struct{}),
			firstSaved: make(chan struct{}),
		}
		h := &Handler{scopes: book}
		hr := cs3reader.Resource{ID: "s$sp!hr", Path: "HR", IsFolder: true}
		specs := cs3reader.Resource{ID: "s$sp!specs", Path: "Specs", IsFolder: true}

		firstDone := make(chan error, 1)
		go func() {
			_, err := h.commitReadScope(context.Background(), "ada", "s$sp", hr, false)
			firstDone <- err
		}()
		synctest.Wait()
		select {
		case <-book.firstSaved:
		default:
			t.Fatal("the first update did not reach its save")
		}

		secondDone := make(chan error, 1)
		go func() {
			_, err := h.commitReadScope(context.Background(), "ada", "s$sp", specs, true)
			secondDone <- err
		}()
		synctest.Wait()
		select {
		case err := <-secondDone:
			t.Fatalf("the second update finished while the first still held the lock: %v", err)
		default:
		}

		close(book.release)
		synctest.Wait()
		if err := <-firstDone; err != nil {
			t.Fatal(err)
		}
		if err := <-secondDone; err != nil {
			t.Fatal(err)
		}
		book.mu.Lock()
		defer book.mu.Unlock()
		for _, folder := range book.published.Folders {
			if folder.Path == "HR" {
				t.Fatalf("HR was restored: %+v", book.published)
			}
		}
		if len(book.published.Folders) != 1 || book.published.Folders[0].Path != "Specs" {
			t.Fatalf("got %+v, want Specs", book.published)
		}
	})
}
