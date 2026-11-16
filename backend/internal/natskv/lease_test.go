package natskv

import (
	"errors"
	"fmt"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
)

func TestKeyExists(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"single server", &jetstream.APIError{ErrorCode: jetstream.JSErrCodeStreamWrongLastSequence}, true},
		{"replicated bucket", &jetstream.APIError{ErrorCode: jetstream.JSErrCodeStreamWrongLastSequenceConstant}, true},
		{"mapped by nats.go", jetstream.ErrKeyExists, true},
		{"another API error", &jetstream.APIError{ErrorCode: jetstream.JSErrCodeStreamNotFound}, false},
		{"not an API error", errors.New("timeout"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := KeyExists(fmt.Errorf("create: %w", tc.err)); got != tc.want {
				t.Fatalf("KeyExists = %v, want %v", got, tc.want)
			}
		})
	}
}
