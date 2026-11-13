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
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
)

func (f fakeFiles) StatPath(_ context.Context, relPath string) (cs3reader.Resource, error) {
	for _, r := range f.resources {
		if r.Path == strings.Trim(relPath, "/") {
			return r, nil
		}
	}
	return cs3reader.Resource{}, cs3reader.ErrNotFound
}

func TestScopeAllows(t *testing.T) {
	hr := cs3reader.Resource{ID: "s$sp!hr", Path: "HR", IsFolder: true}
	hr2026 := cs3reader.Resource{ID: "s$sp!hr-2026", Path: "HR/2026", IsFolder: true}
	hrLookalike := cs3reader.Resource{ID: "s$sp!hrx", Path: "HRx", IsFolder: true}
	invoices := cs3reader.Resource{ID: "s$sp!invoices", Path: "Invoices", IsFolder: true}
	private := cs3reader.Resource{ID: "s$sp!private", Path: "Private", IsFolder: true}
	watched := map[string]bool{invoices.ID: true}
	named := readScope{Mode: scopeAllowed, Folders: []scopeFolder{{ID: hr.ID, Path: "HR"}}}
	byPath := readScope{Mode: scopeAllowed, Folders: []scopeFolder{{Path: "/HR/"}}}
	cases := []struct {
		name   string
		scope  readScope
		folder cs3reader.Resource
		want   bool
	}{
		{"watched mode allows a watched folder", readScope{Mode: scopeWatched}, invoices, true},
		{"watched mode refuses an unwatched folder", readScope{Mode: scopeWatched}, hr, false},
		{"watched mode ignores named folders", readScope{Mode: scopeWatched, Folders: named.Folders}, hr, false},
		{"an empty mode behaves as watched", readScope{}, private, false},
		{"allowed mode allows a named folder", named, hr, true},
		{"allowed mode allows a folder named by path only", byPath, hr, true},
		{"allowed mode allows a subfolder of a named one", named, hr2026, true},
		{"a name that only starts like a named folder is not inside it", named, hrLookalike, false},
		{"allowed mode still allows a watched folder", named, invoices, true},
		{"allowed mode refuses the rest", named, private, false},
		{"all mode allows everything", readScope{Mode: scopeAll}, private, true},
		{"no id is never allowed", readScope{Mode: scopeAll}, cs3reader.Resource{Path: "Private"}, false},
		{"an unknown mode is refused", readScope{Mode: "sure"}, invoices, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scopeAllows(tc.scope, watched, tc.folder); got != tc.want {
				t.Fatalf("scopeAllows() = %v, want %v", got, tc.want)
			}
		})
	}
}

// fakePickupSynaplan answers the pickup routes and the watch list, and
// records what it got.
type fakePickupSynaplan struct {
	*httptest.Server
	claimStatus int
	claim       map[string]any
	watches     []watch
	upload      []string

	mu       sync.Mutex
	listings [][]listingItem
	uploads  []string
	results  []pickupResult
}

func newFakePickupSynaplan(t *testing.T) *fakePickupSynaplan {
	t.Helper()
	f := &fakePickupSynaplan{claimStatus: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/platform-links/pickups/9/claim", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(f.claimStatus)
		_ = json.NewEncoder(w).Encode(f.claim)
	})
	mux.HandleFunc("GET /api/v1/cloud/watches", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": f.watches})
	})
	mux.HandleFunc("POST /api/v1/platform-links/pickups/9/listing", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Items []listingItem `json:"items"`
		}
		if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.listings = append(f.listings, body.Items)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"upload": f.upload, "reuse": []any{}, "skip": []any{}})
	})
	mux.HandleFunc("POST /api/v1/platform-links/pickups/9/files", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		content, _ := io.ReadAll(file)
		f.mu.Lock()
		f.uploads = append(f.uploads, fmt.Sprintf("%s@%s=%s", header.Filename, r.FormValue("version"), content))
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"file_id": 1})
	})
	mux.HandleFunc("POST /api/v1/platform-links/pickups/9/result", func(w http.ResponseWriter, r *http.Request) {
		var result pickupResult
		if json.NewDecoder(r.Body).Decode(&result) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.results = append(f.results, result)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"state": result.Status})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func pickupClaimFor(sub, folderID, folderPath string, scope readScope) map[string]any {
	return map[string]any{
		"id": "9", "external_id": sub, "mode": "fetch",
		"selector":   map[string]any{"folder_id": folderID, "folder_path": folderPath, "pattern": ""},
		"read_scope": scope,
		"limits":     map[string]any{"max_files": 50},
		"expires_at": 0,
	}
}

func TestRunPickup(t *testing.T) {
	inboxNamed := readScope{Mode: scopeAllowed, Folders: []scopeFolder{{Path: "Inbox"}}}
	cases := []struct {
		name        string
		claimStatus int
		claim       map[string]any
		watches     []watch
		upload      []string
		broken      []string
		want        pickupResult
		wantListed  []string
		wantUploads []string
		wantNoClose bool
	}{
		{
			name:        "reads a folder named by path and sends what Synaplan asks for",
			claim:       pickupClaimFor("ada", "", "/Inbox", inboxNamed),
			upload:      []string{"s$sp!a", "s$sp!b"},
			want:        pickupResult{Status: "done", Listed: 2, Sent: 2, Skipped: []pickupSkip{}},
			wantListed:  []string{"s$sp!a", "s$sp!b"},
			wantUploads: []string{"a.txt@ea=alpha", "b.pdf@eb=%PDF"},
		},
		{
			name:        "reads a watched folder by id with the default setting",
			claim:       pickupClaimFor("ada", "s$sp!inbox", "", readScope{Mode: scopeWatched}),
			watches:     []watch{{ID: 1, TargetKind: "folder", TargetID: "s$sp!inbox", State: "active"}},
			upload:      []string{"s$sp!a"},
			want:        pickupResult{Status: "done", Listed: 2, Sent: 1, Skipped: []pickupSkip{}},
			wantListed:  []string{"s$sp!a", "s$sp!b"},
			wantUploads: []string{"a.txt@ea=alpha"},
		},
		{
			name:    "a paused watch does not allow a read",
			claim:   pickupClaimFor("ada", "s$sp!inbox", "", readScope{Mode: scopeWatched}),
			watches: []watch{{ID: 1, TargetKind: "folder", TargetID: "s$sp!inbox", State: "paused"}},
			want:    failedPickup("not_allowed"),
		},
		{
			name:  "a path the person did not allow is not listed",
			claim: pickupClaimFor("ada", "", "Inbox/Archive", readScope{Mode: scopeAllowed, Folders: []scopeFolder{{Path: "Elsewhere"}}}),
			want:  failedPickup("not_allowed"),
		},
		{
			name:  "an id is judged by the path it really has",
			claim: pickupClaimFor("ada", "s$sp!sub", "Elsewhere", readScope{Mode: scopeAllowed, Folders: []scopeFolder{{Path: "Elsewhere"}}}),
			want:  failedPickup("not_allowed"),
		},
		{
			name:  "a file is not a folder",
			claim: pickupClaimFor("ada", "s$sp!c", "", readScope{Mode: scopeAll}),
			want:  failedPickup("not_found"),
		},
		{
			name:  "an unknown path is not found",
			claim: pickupClaimFor("ada", "", "Nowhere", readScope{Mode: scopeAll}),
			want:  failedPickup("not_found"),
		},
		{
			name:  "a claim for another person reads nothing",
			claim: pickupClaimFor("eve", "s$sp!inbox", "", readScope{Mode: scopeAll}),
			want:  failedPickup("not_allowed"),
		},
		{
			name:        "a file that cannot be read makes the read partial",
			claim:       pickupClaimFor("ada", "s$sp!inbox", "", readScope{Mode: scopeAll}),
			upload:      []string{"s$sp!a", "s$sp!b", "s$sp!gone"},
			broken:      []string{"s$sp!b"},
			want:        pickupResult{Status: "partial", Listed: 2, Sent: 1, Skipped: []pickupSkip{{ID: "s$sp!b", Reason: "unreachable"}, {ID: "s$sp!gone", Reason: "not_found"}}},
			wantListed:  []string{"s$sp!a", "s$sp!b"},
			wantUploads: []string{"a.txt@ea=alpha"},
		},
		{
			name:        "a pickup claimed elsewhere is left alone",
			claimStatus: http.StatusConflict,
			claim:       map[string]any{"success": false, "error": "already_claimed"},
			wantNoClose: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synaplan := newFakePickupSynaplan(t)
			if tc.claimStatus != 0 {
				synaplan.claimStatus = tc.claimStatus
			}
			synaplan.claim = tc.claim
			synaplan.watches = tc.watches
			synaplan.upload = tc.upload
			h, err := New(nil, synaplan.URL, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			files := inbox()
			for _, id := range tc.broken {
				files.broken[id] = true
			}

			got := h.runPickup(context.Background(), files, "ada", 9)

			if tc.wantNoClose {
				if len(synaplan.results) != 0 || len(synaplan.listings) != 0 {
					t.Fatalf("listed %v and closed %v a pickup that was not claimed", synaplan.listings, synaplan.results)
				}
				return
			}
			if !equalResults(got, tc.want) || len(synaplan.results) != 1 || !equalResults(synaplan.results[0], tc.want) {
				t.Fatalf("result %+v, Synaplan got %+v, want %+v", got, synaplan.results, tc.want)
			}
			var listed []string
			for _, listing := range synaplan.listings {
				for _, item := range listing {
					listed = append(listed, item.ID)
					if item.Folder != "Inbox" {
						t.Fatalf("item %s listed in folder %q", item.ID, item.Folder)
					}
				}
			}
			if !slices.Equal(listed, tc.wantListed) {
				t.Fatalf("listed %v, want %v", listed, tc.wantListed)
			}
			if !slices.Equal(synaplan.uploads, tc.wantUploads) {
				t.Fatalf("uploaded %v, want %v", synaplan.uploads, tc.wantUploads)
			}
		})
	}
}

func equalResults(a, b pickupResult) bool {
	return a.Status == b.Status && a.Listed == b.Listed && a.Sent == b.Sent && a.ErrorCode == b.ErrorCode && slices.Equal(a.Skipped, b.Skipped)
}

func TestInboundPickupThatCannotStartIsLeftForTheRetry(t *testing.T) {
	synaplan := newSignedSynaplan(t)
	defer synaplan.Close()
	h, err := New(nil, synaplan.URL, nil, delegated(t, fakeIdentityProvider(t, "ada"), "ada"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		claims map[string]any
		status int
		want   string
	}{
		{"a person who did not allow work while away", map[string]any{"type": "pickup", "id": "9", "sub": "eve"}, http.StatusConflict, "not_connected"},
		{"files that cannot be read without the person", map[string]any{"type": "pickup", "id": "10", "sub": "ada"}, http.StatusServiceUnavailable, "files_unreachable"},
		{"a pickup id that is not a number", map[string]any{"type": "pickup", "id": "x", "sub": "ada"}, http.StatusBadRequest, "invalid_pickup"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := synaplan.notification(t, tc.claims)
			rec := inbound(h, token)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("status %d %s, want %d %s", rec.Code, rec.Body, tc.status, tc.want)
			}
			if tc.status == http.StatusBadRequest {
				return
			}
			if again := inbound(h, token); again.Code != tc.status {
				t.Fatalf("retry: status %d %s, want %d", again.Code, again.Body, tc.status)
			}
		})
	}
}
