package handler

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
)

func TestPublicFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
	}{
		{"empty file", errFileEmpty, "file_empty"},
		{"unsupported type", fmt.Errorf("%w: %q", errUnsupportedType, "image/png"), "unsupported_type"},
		{"no text", fmt.Errorf("knowledge: %w", errNoText), "no_text"},
		{"rate limited", errRateLimited, "rate_limited"},
		{"upstream 429", wrapUpstreamStatus("upload", http.StatusTooManyRequests, []byte(`{"error":"slow down"}`)), "rate_limited"},
		{"upstream 4xx", wrapUpstreamStatus("summary/generate", 400, []byte("Exception at /var/www")), "synaplan_error"},
		{"anything else", errors.New("dial tcp: connection refused"), "synaplan_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := publicFailure(tc.err)
			if got.Code != tc.code {
				t.Fatalf("code = %q, want %q", got.Code, tc.code)
			}
			if strings.Contains(got.Error, tc.err.Error()) || strings.Contains(got.Error, "Exception") {
				t.Fatalf("the sentence leaks the error: %q", got.Error)
			}
		})
	}
}

func TestReadableFiles(t *testing.T) {
	cases := []struct {
		name    string
		file    *cs3reader.File
		asText  bool
		extract bool
	}{
		{"plain text", &cs3reader.File{Name: "a.txt", MimeType: "text/plain"}, true, false},
		{"markdown by mime", &cs3reader.File{Name: "a.bin", MimeType: "text/markdown"}, true, false},
		{"text name without a text mime", &cs3reader.File{Name: "notes/a.MD", MimeType: "application/octet-stream"}, true, false},
		{"csv name", &cs3reader.File{Name: "a.csv", MimeType: ""}, true, false},
		{"note is extracted, not read as json", &cs3reader.File{Name: "note1.ocnote", MimeType: "application/json"}, false, true},
		{"note stored as octet-stream", &cs3reader.File{Name: "/Inbox/note1.OCNOTE", MimeType: "application/octet-stream"}, false, true},
		{"pdf", &cs3reader.File{Name: "a.pdf", MimeType: "application/pdf"}, false, true},
		{"pdf named like text still extracts", &cs3reader.File{Name: "a.txt", MimeType: "application/pdf"}, false, true},
		{"image", &cs3reader.File{Name: "a.png", MimeType: "image/png"}, false, false},
		{"unknown name and mime", &cs3reader.File{Name: "a.bin", MimeType: "application/octet-stream"}, false, false},
		{"nil", nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := readsAsText(tc.file); got != tc.asText {
				t.Fatalf("readsAsText = %v, want %v", got, tc.asText)
			}
			if got := needsExtraction(tc.file); got != tc.extract {
				t.Fatalf("needsExtraction = %v, want %v", got, tc.extract)
			}
		})
	}
}

func TestWriteFailureStatusCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"empty file", errFileEmpty, http.StatusUnprocessableEntity},
		{"unsupported type", errUnsupportedType, http.StatusUnprocessableEntity},
		{"no text", errNoText, http.StatusUnprocessableEntity},
		{"rate limited", errRateLimited, http.StatusTooManyRequests},
		{"upstream 5xx", errors.New("boom"), http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeFailure(rr, "test", tc.err)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d", rr.Code, tc.want)
			}
		})
	}
}
