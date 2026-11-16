package natskv

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// LeasePoll is how often a waiting replica tries a held lease again.
const LeasePoll = 100 * time.Millisecond

// Lease holds key exclusively across every replica of this service,
// waiting while another holder has it. The key is created with ttl, so
// NATS removes a lease whose replica died without giving it back, and no
// replica compares clocks; the bucket needs a LimitMarkerTTL (NATS 2.11).
// Work under the lease uses held, which ends after hold — keep hold well
// below ttl so the lease cannot lapse while the work still runs. release
// gives the lease back and purges its marker, so lease keys do not pile up.
func Lease(ctx context.Context, kv jetstream.KeyValue, key string, ttl, hold time.Duration) (held context.Context, release func(), err error) {
	for {
		rev, err := kv.Create(ctx, key, []byte("held"), jetstream.KeyTTL(ttl))
		if err == nil {
			held, cancel := context.WithTimeout(ctx, hold)
			return held, func() {
				cancel()
				done, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer stop()
				_ = kv.Purge(done, key, jetstream.LastRevision(rev), jetstream.PurgeTTL(ttl))
			}, nil
		}
		if !KeyExists(err) {
			return nil, nil, fmt.Errorf("taking a lease: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("waiting for a lease: %w", ctx.Err())
		case <-time.After(LeasePoll):
		}
	}
}

// KeyExists reports a Create that lost to a key someone else holds. Over a
// deleted key, which every given-back lease leaves, nats.go does not map a
// replicated bucket's answer (10164) to ErrKeyExists, so the code is read.
func KeyExists(err error) bool {
	var apiErr *jetstream.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence ||
			apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequenceConstant
	}
	return errors.Is(err, jetstream.ErrKeyExists)
}
