package handler

import (
	"errors"
	"fmt"
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
