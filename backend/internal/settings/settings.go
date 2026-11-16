// Package settings keeps the choices an OpenCloud administrator makes for
// Synaplan on the Synaplan page. They apply to everyone on this OpenCloud
// and to every replica of this service, so they live in OpenCloud's NATS
// key-value store, next to the offline tokens, not in a file of this
// service.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/metadist/synaplan-opencloud/internal/natskv"
)

// Values are the administrator's choices. They are written together, so
// a stored value replaces the configured defaults as a whole.
type Values struct {
	// DefaultAnswerLanguage is the ISO 639-1 code answers come in.
	DefaultAnswerLanguage string `json:"defaultAnswerLanguage"`
	// UseUserLanguage answers in each person's OpenCloud interface
	// language when it is supported, with the default as the fallback.
	UseUserLanguage bool `json:"useUserLanguage"`
}

// Store keeps one set of Values for this OpenCloud.
type Store interface {
	// Get returns the stored values. ok is false when the administrator
	// never saved any.
	Get(ctx context.Context) (values Values, ok bool, err error)
	// Put replaces the stored values. The last write wins.
	Put(ctx context.Context, values Values) error
	// Close ends the connection to the store, if there is one.
	Close() error
}

// key is the one key in the bucket.
const key = "admin"

// NATSStore keeps the values in a bucket of OpenCloud's NATS.
type NATSStore struct {
	bucket *natskv.Bucket
}

// NewNATS prepares the bucket. Like the token store it does not wait for
// NATS; until NATS answers, Get and Put fail.
func NewNATS(cfg natskv.Config, bucket string, replicas int) (*NATSStore, error) {
	b, err := natskv.New(cfg, jetstream.KeyValueConfig{
		Bucket:      bucket,
		Description: "Administrator settings of the Synaplan app for OpenCloud",
		History:     1,
		Storage:     jetstream.FileStorage,
		Replicas:    replicas,
	})
	if err != nil {
		return nil, fmt.Errorf("settings: %w", err)
	}
	return &NATSStore{bucket: b}, nil
}

// Get reads the values.
func (s *NATSStore) Get(ctx context.Context) (Values, bool, error) {
	kv, err := s.bucket.Open(ctx)
	if err != nil {
		return Values{}, false, fmt.Errorf("settings: %w", err)
	}
	entry, err := kv.Get(ctx, key)
	if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
		return Values{}, false, nil
	}
	if err != nil {
		return Values{}, false, fmt.Errorf("settings: reading: %w", err)
	}
	var values Values
	if err := json.Unmarshal(entry.Value(), &values); err != nil {
		return Values{}, false, fmt.Errorf("settings: decoding: %w", err)
	}
	return values, true, nil
}

// Put writes the values.
func (s *NATSStore) Put(ctx context.Context, values Values) error {
	kv, err := s.bucket.Open(ctx)
	if err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("settings: encoding: %w", err)
	}
	if _, err := kv.Put(ctx, key, raw); err != nil {
		return fmt.Errorf("settings: writing: %w", err)
	}
	return nil
}

// Close disconnects from NATS.
func (s *NATSStore) Close() error {
	if s == nil {
		return nil
	}
	return s.bucket.Close()
}

// MemoryStore keeps the values in this process. A restart forgets them
// and another replica never sees them.
type MemoryStore struct {
	mu     sync.Mutex
	values *Values
}

// NewMemoryStore returns a MemoryStore with nothing saved.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// Get reads the values.
func (s *MemoryStore) Get(context.Context) (Values, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		return Values{}, false, nil
	}
	return *s.values, true, nil
}

// Put writes the values.
func (s *MemoryStore) Put(_ context.Context, values Values) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values = &values
	return nil
}

// Close does nothing.
func (s *MemoryStore) Close() error { return nil }
