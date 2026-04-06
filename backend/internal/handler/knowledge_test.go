package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
