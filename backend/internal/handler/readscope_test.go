package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

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
			got, err := withFolder(tc.scope, hr, tc.allow)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestReadScopeRoutesRefuseWithoutDelegation(t *testing.T) {
	shared := &Handler{}
	shared.UseSharedKey()
	cases := []struct {
		name string
		h    *Handler
	}{
		{"no organisation login", &Handler{}},
		{"one shared API key", shared},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, req := range []*http.Request{
				httptest.NewRequest(http.MethodGet, "/api/synaplan/read-scope?folderId=s$sp!hr", nil),
				httptest.NewRequest(http.MethodPut, "/api/synaplan/read-scope", strings.NewReader(`{"folderId":"s$sp!hr","allow":true}`)),
			} {
				rec := httptest.NewRecorder()
				if req.Method == http.MethodGet {
					tc.h.ReadScope(rec, req)
				} else {
					tc.h.SetReadScope(rec, req)
				}
				if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "reads_unavailable") {
					t.Fatalf("%s: status %d %s, want 409 reads_unavailable", req.Method, rec.Code, rec.Body)
				}
			}
		})
	}
}

func TestKeptReadScope(t *testing.T) {
	fallback := readScope{Mode: scopeWatched, Folders: []scopeFolder{}}
	named := readScope{Mode: scopeAllowed, Folders: []scopeFolder{{ID: "s$sp!hr", Path: "HR"}}}
	cases := []struct {
		name    string
		kept    string
		want    readScope
		wantRev bool
	}{
		{name: "nothing kept is the default", want: fallback},
		{name: "a kept setting", kept: `{"mode":"allowed","folders":[{"id":"s$sp!hr","path":"HR"}]}`, want: named, wantRev: true},
		{name: "a kept setting without folders", kept: `{"mode":"watched"}`, want: fallback, wantRev: true},
		{name: "a kept value that is not JSON is the default and can be replaced", kept: `nope`, want: fallback, wantRev: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := New(nil, "http://synaplan.invalid", nil, delegated(t, fakeIdentityProvider(t, "ada"), ""), Admin{})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if tc.kept != "" {
				if ok, err := h.delegation.PutSetting(ctx, readScopeSetting, "ada", []byte(tc.kept), 0); err != nil || !ok {
					t.Fatalf("keeping: %v %v", ok, err)
				}
			}
			got, rev, err := h.keptReadScope(ctx, "ada")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) || (rev != 0) != tc.wantRev {
				t.Fatalf("got %+v at %d, want %+v (revision kept: %v)", got, rev, tc.want, tc.wantRev)
			}
		})
	}
}

func TestKeptReadScopeWithoutAStore(t *testing.T) {
	if _, _, err := (&Handler{}).keptReadScope(context.Background(), "ada"); err == nil {
		t.Fatal("want an error without a store, so a read is refused rather than allowed")
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
		{"a folder outside the personal space", errNotPersonal, http.StatusConflict, "reads_personal_only"},
		{"another change came first", errReadScopeMoved, http.StatusConflict, "reads_changed"},
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
