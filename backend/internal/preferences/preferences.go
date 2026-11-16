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
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/metadist/synaplan-opencloud/internal/natskv"
)

// Mode is how much of a person's files Synaplan may read.
type Mode string

// The read modes Synaplan knows.
const (
	// ReadWatched lets Synaplan read only what the person watches.
	ReadWatched Mode = "watched"
	// ReadAllowed adds the folders the person allowed.
	ReadAllowed Mode = "allowed"
	// ReadAll lets Synaplan read all of the person's files.
	ReadAll Mode = "all"
)

// ValidMode reports whether mode is one Synaplan accepts.
func ValidMode(mode Mode) bool {
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
	Mode    Mode     `json:"mode"`
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
	// Lease holds subject exclusively across every replica, waiting while
	// another holder has it. A save reports the setting to Synaplan and
	// then keeps it here under one lease, so two saves at once cannot
	// leave this copy different from what Synaplan follows. Work under the
	// lease uses held; release gives the lease back.
	Lease(ctx context.Context, subject string) (held context.Context, release func(), err error)
	// Close ends the connection to the store, if there is one.
	Close() error
}

// MemoryStore keeps the records in this process. A restart forgets them
// and another replica never sees them.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]Preferences
	leases  map[string]chan struct{}
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: map[string]Preferences{}, leases: map[string]chan struct{}{}}
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

// Lease holds subject until release is called, waiting while another
// caller in this process has it.
func (s *MemoryStore) Lease(ctx context.Context, subject string) (context.Context, func(), error) {
	s.mu.Lock()
	lease, ok := s.leases[subject]
	if !ok {
		lease = make(chan struct{}, 1)
		s.leases[subject] = lease
	}
	s.mu.Unlock()
	select {
	case lease <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, fmt.Errorf("preferences: waiting for a lease: %w", ctx.Err())
	}
	held, cancel := context.WithTimeout(ctx, leaseHold)
	return held, func() {
		cancel()
		<-lease
	}, nil
}

// Close does nothing.
func (s *MemoryStore) Close() error { return nil }

var errNoSubject = errors.New("preferences: a record needs a subject")

// leaseTTL is how long NATS keeps a lease whose holder never gave it back,
// because its replica died.
const leaseTTL = 2 * time.Minute

// leaseHold is how long a save under a lease may take: resolving the
// folders, reporting to Synaplan and keeping the copy. It ends well before
// leaseTTL, so the lease cannot lapse while a save is still running.
const leaseHold = leaseTTL / 2

// NATSStore is a JetStream key-value bucket in OpenCloud's NATS. A person's
// record is replaced as a whole, under the person's lease.
type NATSStore struct {
	bucket *natskv.Bucket
}

// NewNATS prepares the bucket in OpenCloud's NATS (see natskv.New). Leases
// expire on their own, which needs NATS 2.11 or later, as the offline
// tokens already do.
func NewNATS(cfg natskv.Config, bucket string, replicas int) (*NATSStore, error) {
	b, err := natskv.New(cfg, jetstream.KeyValueConfig{
		Bucket:         bucket,
		Description:    "Each person's own Synaplan settings",
		History:        1,
		Storage:        jetstream.FileStorage,
		Replicas:       replicas,
		LimitMarkerTTL: leaseTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("preferences: %w", err)
	}
	return &NATSStore{bucket: b}, nil
}

func (s *NATSStore) open(ctx context.Context) (jetstream.KeyValue, error) {
	kv, err := s.bucket.Open(ctx)
	if err != nil {
		return nil, fmt.Errorf("preferences: %w", err)
	}
	return kv, nil
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

// Lease holds the person's lease key in this bucket (see natskv.Lease).
func (s *NATSStore) Lease(ctx context.Context, subject string) (context.Context, func(), error) {
	if subject == "" {
		return nil, nil, errNoSubject
	}
	kv, err := s.open(ctx)
	if err != nil {
		return nil, nil, err
	}
	held, release, err := natskv.Lease(ctx, kv, "lease."+key(subject), leaseTTL, leaseHold)
	if err != nil {
		return nil, nil, fmt.Errorf("preferences: %w", err)
	}
	return held, release, nil
}

// Close disconnects from NATS.
func (s *NATSStore) Close() error {
	if s == nil {
		return nil
	}
	return s.bucket.Close()
}

// key encodes subject so it is one NATS key: a subject may contain
// characters NATS rejects in a key, such as a dot or an at-sign.
func key(subject string) string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(subject))
}
