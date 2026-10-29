package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencloud-eu/opencloud/pkg/middleware"
	"go-micro.dev/v4/metadata"

	"github.com/metadist/synaplan-opencloud/internal/state"
)

func stateAdmin(language string, useUser, allow *bool) state.Admin {
	return state.Admin{DefaultAnswerLanguage: language, UseUserLanguage: useUser, AllowMemories: allow}
}

func withRoles(ctx context.Context, ids ...string) context.Context {
	raw, _ := json.Marshal(ids)
	return metadata.Set(ctx, middleware.RoleIDs, string(raw))
}

func TestResolveLanguageFollowsTheAdministrator(t *testing.T) {
	store, err := state.Open("", "")
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{settings: Settings{DefaultAnswerLanguage: "de", UseUserLanguage: true}, state: store}
	if got := h.resolveLanguage("fr-FR"); got != "fr" {
		t.Fatalf("interface language = %s, want fr", got)
	}
	if got := h.resolveLanguage("xx"); got != "de" {
		t.Fatalf("unsupported interface language = %s, want the default de", got)
	}
	if got := h.resolveLanguage(""); got != "de" {
		t.Fatalf("no interface language = %s, want de", got)
	}

	off := false
	if err := h.store().PutAdmin(stateAdmin("tr", &off, nil)); err != nil {
		t.Fatal(err)
	}
	if got := h.resolveLanguage("fr"); got != "tr" {
		t.Fatalf("after the administrator turned the interface language off = %s, want tr", got)
	}
}

func TestSaveSettingsNeedsTheAdminRole(t *testing.T) {
	store, err := state.Open("", "")
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{state: store}
	body := `{"defaultAnswerLanguage":"de","useUserLanguage":false,"allowMemories":false}`

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/synaplan/settings", strings.NewReader(body))
	req = req.WithContext(withRoles(req.Context(), "some-other-role"))
	h.SaveSettings(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d, want 403", rr.Code)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/synaplan/settings", strings.NewReader(body))
	req = req.WithContext(withRoles(req.Context(), adminRoleID))
	h.SaveSettings(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin status = %d body %s", rr.Code, rr.Body.String())
	}
	if got := h.adminSettings(); got.DefaultAnswerLanguage != "de" || got.UseUserLanguage || got.AllowMemories {
		t.Fatalf("settings after save = %+v", got)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/synaplan/settings", strings.NewReader(`{"defaultAnswerLanguage":"klingon"}`))
	req = req.WithContext(withRoles(req.Context(), adminRoleID))
	h.SaveSettings(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown language status = %d, want 400", rr.Code)
	}
}
