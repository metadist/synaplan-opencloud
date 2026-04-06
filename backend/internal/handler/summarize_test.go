package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Summarize's validation path runs before any CS3 or Synaplan calls,
// so we can exercise it against a bare Handler value. Anything past
// validation is covered by E2E.

func TestSummarize_RejectsBadJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/synaplan/summarize", strings.NewReader("{not json"))
	(&Handler{}).Summarize(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid JSON body") {
		t.Errorf("body = %q, want contains 'invalid JSON body'", rr.Body.String())
	}
}

func TestSummarize_RejectsMissingFields(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{"no resourceId", `{"summaryType":"abstractive","length":"short"}`, "resourceId is required"},
		{"bad summaryType", `{"resourceId":"r1","summaryType":"garbage","length":"short"}`, "unsupported summaryType"},
		{"custom length not allowed", `{"resourceId":"r1","summaryType":"abstractive","length":"custom"}`, "unsupported length"},
		{"empty length", `{"resourceId":"r1","summaryType":"abstractive"}`, "unsupported length"},
		{"empty summaryType", `{"resourceId":"r1","length":"short"}`, "unsupported summaryType"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/synaplan/summarize", strings.NewReader(tc.body))
			(&Handler{}).Summarize(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rr.Code)
			}
			if !strings.Contains(rr.Body.String(), tc.wantMsg) {
				t.Errorf("body = %q, want contains %q", rr.Body.String(), tc.wantMsg)
			}
		})
	}
}

func TestSummarize_AcceptsAllAllowedTypeAndLengthCombos(t *testing.T) {
	// Validation must pass for every supported combo. With a bare
	// Handler (nil cs3), the pipeline fails at the cs3.Open step
	// and the runner writes a 502 — anything non-400 here proves
	// we got past validation.
	types := []string{"abstractive", "bullet-points", "extractive"}
	lengths := []string{"short", "medium", "long"}
	for _, st := range types {
		for _, l := range lengths {
			t.Run(st+"/"+l, func(t *testing.T) {
				rr := httptest.NewRecorder()
				body := `{"resourceId":"r1","summaryType":"` + st + `","length":"` + l + `"}`
				req := httptest.NewRequest(http.MethodPost, "/api/synaplan/summarize", strings.NewReader(body))
				(&Handler{}).Summarize(rr, req)

				if rr.Code == http.StatusBadRequest {
					t.Errorf("validation rejected %s/%s: %s", st, l, rr.Body.String())
				}
			})
		}
	}
}
