package handler

import (
	"errors"
	"fmt"
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

func TestClientInputFailuresStayClientInput(t *testing.T) {
	for _, err := range []error{errFileEmpty, errUnsupportedType, errNoText} {
		if !errors.Is(err, errClientInput) {
			t.Fatalf("%v is not client input, it would answer 502", err)
		}
	}
}
