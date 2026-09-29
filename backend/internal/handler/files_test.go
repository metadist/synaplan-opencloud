package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestSaveToSpace_RejectsBadInput(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{"bad json", "{", "invalid JSON body"},
		{"no filename", `{"text":"hello"}`, "filename is required"},
		{"no text", `{"filename":"note.md"}`, "text is required"},
		{"blank text", `{"filename":"note.md","text":"  "}`, "text is required"},
		{"dot name", `{"filename":".","text":"hello"}`, "filename is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/synaplan/files", strings.NewReader(tc.body))
			(&Handler{}).SaveToSpace(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body %s", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), tc.wantMsg) {
				t.Errorf("body = %q, want contains %q", rr.Body.String(), tc.wantMsg)
			}
		})
	}
}

func TestKnowledgeStatus_RequiresResourceID(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/synaplan/knowledge/status", nil)
	(&Handler{}).KnowledgeStatus(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestRemoveFromKnowledge_RejectsBadID(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/synaplan/knowledge/0", nil)
	req = reqWithChiParam(req, "fileId", "0")
	(&Handler{}).RemoveFromKnowledge(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

func TestParseSynaplanAccount(t *testing.T) {
	got := parseSynaplanAccount([]byte(`{"success":true,"user":{"email":"ada@example.com","firstName":"Ada","level":"PRO"}}`))
	if got == nil || got.Email != "ada@example.com" || got.FirstName != "Ada" {
		t.Fatalf("account = %+v", got)
	}
	if parseSynaplanAccount([]byte(`{"success":false}`)) != nil {
		t.Fatal("expected nil account")
	}
}

func reqWithChiParam(req *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestCleanSavedName(t *testing.T) {
	name, msg := cleanSavedName(`../Summary of report.md`)
	if msg != "" || name != "Summary of report.md" {
		t.Fatalf("name=%q msg=%q", name, msg)
	}
	if _, msg := cleanSavedName("   "); msg == "" {
		t.Fatal("expected rejection")
	}
}
