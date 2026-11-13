package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/preferences"
)

// fakeFolders knows folders by id; any other id is no folder, and "broken"
// is a gateway that does not answer.
type fakeFolders map[string]string

func (f fakeFolders) FolderPath(_ context.Context, id string) (string, error) {
	if id == "broken" {
		return "", errors.New("gateway down")
	}
	path, ok := f[id]
	if !ok {
		return "", cs3reader.ErrNoFolder
	}
	return path, nil
}

type failingPreferences struct{ getErr, putErr error }

func (s failingPreferences) Get(context.Context, string) (preferences.Preferences, error) {
	return preferences.Default(), s.getErr
}
func (s failingPreferences) Put(context.Context, string, preferences.Preferences) error {
	return s.putErr
}
func (s failingPreferences) Close() error { return nil }

// preferencesHandler is a handler for ada and eve whose Synaplan answers
// the read scope with status and records what it was sent.
func preferencesHandler(t *testing.T, store preferences.Store, status int) (*Handler, *[]map[string]any) {
	t.Helper()
	var reported []map[string]any
	synaplan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/platform-links/read-scope" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		reported = append(reported, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(synaplan.Close)
	d := delegated(t, fakeIdentityProvider(t, "ada"), "")
	d.Preferences = store
	h, err := New(nil, synaplan.URL, nil, d)
	if err != nil {
		t.Fatal(err)
	}
	h.folders = fakeFolders{"s$p!specs": "Specs", "s$p!inv": "Invoices/2026"}
	return h, &reported
}

func save(h *Handler, userID, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.SavePreferences(rec, httptest.NewRequestWithContext(signedIn(userID, tokenFor(strings.TrimPrefix(userID, "u-"))), http.MethodPut, "/api/synaplan/preferences", strings.NewReader(body)))
	return rec
}

func load(t *testing.T, h *Handler, userID string) (int, preferencesAnswer) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Preferences(rec, httptest.NewRequestWithContext(signedIn(userID, tokenFor(strings.TrimPrefix(userID, "u-"))), http.MethodGet, "/api/synaplan/preferences", nil))
	var got preferencesAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("answer %s: %v", rec.Body, err)
	}
	return rec.Code, got
}

func TestAPersonChoosesWhatSynaplanMayRead(t *testing.T) {
	h, reported := preferencesHandler(t, preferences.NewMemoryStore(), http.StatusOK)

	if code, got := load(t, h, "u-ada"); code != http.StatusOK || !got.Available || got.ReadScope.Mode != "watched" || len(got.ReadScope.Folders) != 0 {
		t.Fatalf("before: %d %+v", code, got)
	}

	rec := save(h, "u-ada", `{"mode":"allowed","folders":[{"id":"s$p!specs","path":"Everything"},{"id":"s$p!specs"},{"id":"s$p!inv","path":"x"},{"id":"","path":"/"},{"id":"s$p!gone","path":"Everything"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	want := []preferences.Folder{{ID: "s$p!specs", Path: "Specs"}, {ID: "s$p!inv", Path: "Invoices/2026"}}
	if _, got := load(t, h, "u-ada"); got.ReadScope.Mode != "allowed" || !reflect.DeepEqual(got.ReadScope.Folders, want) {
		t.Fatalf("after: %+v", got)
	}
	if _, eve := load(t, h, "u-eve"); eve.ReadScope.Mode != "watched" {
		t.Fatalf("eve sees ada's setting: %+v", eve)
	}

	if len(*reported) != 1 {
		t.Fatalf("reported %d times", len(*reported))
	}
	sent := (*reported)[0]
	folders, _ := json.Marshal(sent["folders"])
	if sent["external_id"] != "ada" || sent["mode"] != "allowed" || string(folders) != `[{"id":"s$p!specs","path":"Specs"},{"id":"s$p!inv","path":"Invoices/2026"}]` {
		t.Fatalf("Synaplan received %v", sent)
	}
}

func TestSavePreferencesRefuses(t *testing.T) {
	tooMany := `{"mode":"allowed","folders":[` + strings.Repeat(`{"id":"s$p!specs"},`, maxReadFolders) + `{"id":"s$p!specs"}]}`
	cases := []struct {
		name     string
		store    preferences.Store
		synaplan int
		userID   string
		body     string
		status   int
		code     string
		kept     bool
	}{
		{name: "not json", body: `{`, status: http.StatusBadRequest},
		{name: "an unknown mode", body: `{"mode":"everything"}`, status: http.StatusBadRequest},
		{name: "too many folders", body: tooMany, status: http.StatusBadRequest},
		{name: "a forged bearer", userID: "u-mallory", body: `{"mode":"all"}`, status: http.StatusBadGateway, code: "identity_check_failed"},
		{name: "a gateway that does not answer", body: `{"mode":"allowed","folders":[{"id":"broken"}]}`, status: http.StatusBadGateway, code: "folder_check_failed"},
		{name: "a cloud Synaplan does not know", synaplan: http.StatusForbidden, body: `{"mode":"all"}`, status: http.StatusBadGateway, code: "cloud_not_linked"},
		{name: "links turned off in Synaplan", synaplan: http.StatusNotFound, body: `{"mode":"all"}`, status: http.StatusBadGateway, code: "cloud_not_linked"},
		{name: "a cloud known twice", synaplan: http.StatusConflict, body: `{"mode":"all"}`, status: http.StatusBadGateway, code: "cloud_ambiguous"},
		{name: "a failing Synaplan", synaplan: http.StatusInternalServerError, body: `{"mode":"all"}`, status: http.StatusBadGateway, code: "not_reported"},
		{name: "a store that is down", store: failingPreferences{putErr: errors.New("nats down")}, body: `{"mode":"all"}`, status: http.StatusBadGateway, code: "settings_not_kept"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := tc.store
			if store == nil {
				store = preferences.NewMemoryStore()
			}
			synaplan := tc.synaplan
			if synaplan == 0 {
				synaplan = http.StatusOK
			}
			userID := tc.userID
			if userID == "" {
				userID = "u-ada"
			}
			h, _ := preferencesHandler(t, store, synaplan)
			rec := save(h, userID, tc.body)
			var got errorResponse
			_ = json.Unmarshal(rec.Body.Bytes(), &got)
			if rec.Code != tc.status || got.Code != tc.code {
				t.Fatalf("got %d %s, want %d %q", rec.Code, rec.Body, tc.status, tc.code)
			}
			if mem, ok := store.(*preferences.MemoryStore); ok {
				if p, _ := mem.Get(context.Background(), "ada"); p.ReadScope.Mode != "watched" {
					t.Fatalf("kept %+v after a refused save", p)
				}
			}
		})
	}
}

func TestPreferencesWithoutAPerson(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	h.Preferences(rec, httptest.NewRequest(http.MethodGet, "/api/synaplan/preferences", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"available":false}` {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	h.SavePreferences(rec, httptest.NewRequest(http.MethodPut, "/api/synaplan/preferences", strings.NewReader(`{"mode":"all"}`)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
}

func TestPreferencesStoreDown(t *testing.T) {
	h, _ := preferencesHandler(t, failingPreferences{getErr: errors.New("nats down")}, http.StatusOK)
	rec := httptest.NewRecorder()
	h.Preferences(rec, httptest.NewRequestWithContext(signedIn("u-ada", tokenFor("ada")), http.MethodGet, "/api/synaplan/preferences", nil))
	var got errorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusBadGateway || got.Code != "settings_unavailable" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestConnection(t *testing.T) {
	cases := []struct {
		name string
		h    *Handler
		want string
	}{
		{"shared key", &Handler{}, `{"login":"shared-key"}`},
		{"person login", &Handler{exchanger: exchanger{}}, `{"login":"person"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.h.Connection(rec, httptest.NewRequest(http.MethodGet, "/api/synaplan/connection", nil))
			if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != tc.want {
				t.Fatalf("got %d %s", rec.Code, rec.Body)
			}
		})
	}
}
