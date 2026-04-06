package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Translate's validation path runs before any CS3 or Synaplan calls,
// so we can exercise it against a bare Handler value. Anything past
// validation is covered by E2E.

func TestTranslate_RejectsBadJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/synaplan/translate", strings.NewReader("{not json"))
	(&Handler{}).Translate(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid JSON body") {
		t.Errorf("body = %q, want contains 'invalid JSON body'", rr.Body.String())
	}
}

func TestTranslate_RejectsBadInput(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{"no resourceId", `{"targetLanguage":"de"}`, "resourceId is required"},
		{"unsupported language", `{"resourceId":"r1","targetLanguage":"xx"}`, "unsupported target language"},
		{"empty language", `{"resourceId":"r1"}`, "unsupported target language"},
		{"bad length", `{"resourceId":"r1","targetLanguage":"de","length":"garbage"}`, "unsupported length"},
		{"custom length not allowed", `{"resourceId":"r1","targetLanguage":"de","length":"custom"}`, "unsupported length"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/synaplan/translate", strings.NewReader(tc.body))
			(&Handler{}).Translate(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rr.Code)
			}
			if !strings.Contains(rr.Body.String(), tc.wantMsg) {
				t.Errorf("body = %q, want contains %q", rr.Body.String(), tc.wantMsg)
			}
		})
	}
}

func TestTranslate_AcceptsAllSupportedLanguages(t *testing.T) {
	for _, lang := range []string{"en", "de", "fr", "es", "it"} {
		t.Run(lang, func(t *testing.T) {
			rr := httptest.NewRecorder()
			body := `{"resourceId":"r1","targetLanguage":"` + lang + `"}`
			req := httptest.NewRequest(http.MethodPost, "/api/synaplan/translate", strings.NewReader(body))
			(&Handler{}).Translate(rr, req)

			if rr.Code == http.StatusBadRequest {
				t.Errorf("validation rejected %s: %s", lang, rr.Body.String())
			}
		})
	}
}
