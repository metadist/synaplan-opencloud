// Package preferences keeps each person's own Synaplan settings in this
// cloud: what Synaplan may read of their files. Synaplan receives the
// setting too and enforces it, but it does not hand it back to a cloud, so
// this is the copy the settings page shows. It lives in the key-value
// store OpenCloud already runs, next to the offline tokens.
package preferences

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/metadist/synaplan-opencloud/internal/delegation"
)

// The read modes Synaplan knows.
const (
	// ReadWatched lets Synaplan read only what the person watches.
	ReadWatched = "watched"
	// ReadAllowed adds the folders the person allowed.
	ReadAllowed = "allowed"
	// ReadAll lets Synaplan read all of the person's files.
	ReadAll = "all"
)

// ValidMode reports whether mode is one Synaplan accepts.
func ValidMode(mode string) bool {
	return mode == ReadWatched || mode == ReadAllowed || mode == ReadAll
}

// Folder is a folder the person allowed: its id, and its path as
// OpenCloud knew it when the person allowed it.
type Folder struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// ReadScope is what a person lets Synaplan read.
type ReadScope struct {
	Mode    string   `json:"mode"`
	Folders []Folder `json:"folders"`
}

// Preferences is everything kept for one person.
type Preferences struct {
	ReadScope ReadScope `json:"readScope"`
}

// Default is what a person who never chose has: Synaplan reads only what
// they watch, which is also what Synaplan assumes.
func Default() Preferences {
	return Preferences{ReadScope: ReadScope{Mode: ReadWatched, Folders: []Folder{}}}
}

// normalized fills what an older or partial record leaves out.
func normalized(p Preferences) Preferences {
	if !ValidMode(p.ReadScope.Mode) {
		p.ReadScope.Mode = ReadWatched
	}
	if p.ReadScope.Folders == nil {
		p.ReadScope.Folders = []Folder{}
	}
	return p
}

// Store keeps one record per person, keyed by their subject at the
// identity provider. A person who never saved gets Default.
type Store interface {
	Get(ctx context.Context, subject string) (Preferences, error)
	Put(ctx context.Context, subject string, p Preferences) error
	// Close ends the connection to the store, if there is one.
	Close() error
}

// MemoryStore keeps the records in this process. A restart forgets them
// and another replica never sees them.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]Preferences
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: map[string]Preferences{}}
}

// Get returns the person's record, or Default.
func (s *MemoryStore) Get(_ context.Context, subject string) (Preferences, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.entries[subject]
	if !ok {
		return Default(), nil
	}
	return normalized(p), nil
}

// Put replaces the person's record.
func (s *MemoryStore) Put(_ context.Context, subject string, p Preferences) error {
	if subject == "" {
		return errNoSubject
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[subject] = normalized(p)
	return nil
}

// Close does nothing.
func (s *MemoryStore) Close() error { return nil }

var errNoSubject = errors.New("preferences: a record needs a subject")

// NATSStore is a JetStream key-value bucket in OpenCloud's NATS. A person's
// record is replaced as a whole; the last save wins, as it does for any
// settings page.
type NATSStore struct {
	conn   *nats.Conn
	js     jetstream.JetStream
	bucket jetstream.KeyValueConfig

	mu sync.Mutex
	kv jetstream.KeyValue
}

// NewNATS prepares the bucket in OpenCloud's NATS, reached with the same
// settings as the offline tokens. The bucket is opened, or created, on
// first use, so this service starts while NATS is still starting.
func NewNATS(cfg delegation.NATSConfig) (*NATSStore, error) {
	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		return nil, errors.New("preferences: nats-js-kv needs a bucket")
	}
	if cfg.Replicas < 0 {
		return nil, errors.New("preferences: nats-js-kv replicas must not be negative")
	}
	conn, js, err := delegation.DialNATS(cfg)
	if err != nil {
		return nil, err
	}
	return &NATSStore{conn: conn, js: js, bucket: jetstream.KeyValueConfig{
		Bucket:      bucket,
		Description: "Each person's own Synaplan settings",
		History:     1,
		Storage:     jetstream.FileStorage,
		Replicas:    cfg.Replicas,
	}}, nil
}

func (s *NATSStore) open(ctx context.Context) (jetstream.KeyValue, error) {
	s.mu.Lock()
	kv := s.kv
	s.mu.Unlock()
	if kv != nil {
		return kv, nil
	}
	kv, err := s.js.CreateOrUpdateKeyValue(ctx, s.bucket)
	if err != nil {
		return nil, fmt.Errorf("preferences: opening the %s bucket: %w", s.bucket.Bucket, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kv == nil {
		s.kv = kv
	}
	return s.kv, nil
}

// Get returns the person's record, or Default.
func (s *NATSStore) Get(ctx context.Context, subject string) (Preferences, error) {
	kv, err := s.open(ctx)
	if err != nil {
		return Preferences{}, err
	}
	entry, err := kv.Get(ctx, key(subject))
	if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
		return Default(), nil
	}
	if err != nil {
		return Preferences{}, fmt.Errorf("preferences: reading: %w", err)
	}
	var p Preferences
	if err := json.Unmarshal(entry.Value(), &p); err != nil {
		return Preferences{}, fmt.Errorf("preferences: decoding: %w", err)
	}
	return normalized(p), nil
}

// Put replaces the person's record.
func (s *NATSStore) Put(ctx context.Context, subject string, p Preferences) error {
	if subject == "" {
		return errNoSubject
	}
	value, err := json.Marshal(normalized(p))
	if err != nil {
		return fmt.Errorf("preferences: encoding: %w", err)
	}
	kv, err := s.open(ctx)
	if err != nil {
		return err
	}
	if _, err := kv.Put(ctx, key(subject), value); err != nil {
		return fmt.Errorf("preferences: writing: %w", err)
	}
	return nil
}

// Close disconnects from NATS.
func (s *NATSStore) Close() error {
	if s == nil || s.conn == nil {
		return nil
	}
	s.conn.Close()
	return nil
}

// key encodes subject so it is one NATS key: a subject may contain
// characters NATS rejects in a key, such as a dot or an at-sign.
func key(subject string) string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(subject))
}
