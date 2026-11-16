package handler

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
