package delegation

import (
	"context"
	"fmt"
	"sync"
)

// MemoryStore keeps the offline tokens in this process. Its revisions keep
// increasing across all keys, as a NATS bucket's do. A restart forgets the
// tokens, and another replica never sees them, so it only fits a single
// instance that accepts people allowing again after a restart, and tests.
type MemoryStore struct {
	mu       sync.Mutex
	revision uint64
	entries  map[string]Entry
	leases   map[string]*memLease
}

type memLease struct {
	ch   chan struct{}
	refs int
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: map[string]Entry{}, leases: map[string]*memLease{}}
}

// Get reads the person's token.
func (s *MemoryStore) Get(_ context.Context, subject string) (Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[subject]
	return entry, ok, nil
}

// Write stores the token when match is still current.
func (s *MemoryStore) Write(_ context.Context, subject, token string, match uint64) (uint64, bool, error) {
	if token == "" {
		return 0, false, errEmptyToken
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[subject]
	if (match == 0 && ok) || (match != 0 && (!ok || entry.Revision != match)) {
		return 0, false, nil
	}
	s.revision++
	s.entries[subject] = Entry{Token: token, Revision: s.revision}
	return s.revision, true, nil
}

// Delete removes the token when match is still current.
func (s *MemoryStore) Delete(_ context.Context, subject string, match uint64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[subject]
	if !ok {
		return true, nil
	}
	if entry.Revision != match {
		return false, nil
	}
	delete(s.entries, subject)
	return true, nil
}

// Lease holds subject until release is called, waiting while another
// caller holds it.
func (s *MemoryStore) Lease(ctx context.Context, subject string) (context.Context, func(), error) {
	s.mu.Lock()
	lease, ok := s.leases[subject]
	if !ok {
		lease = &memLease{ch: make(chan struct{}, 1)}
		s.leases[subject] = lease
	}
	lease.refs++
	s.mu.Unlock()

	dropRef := func() {
		s.mu.Lock()
		lease.refs--
		if lease.refs == 0 {
			delete(s.leases, subject)
		}
		s.mu.Unlock()
	}

	select {
	case lease.ch <- struct{}{}:
		var once sync.Once
		held, cancel := context.WithCancel(ctx)
		return held, func() {
			once.Do(func() {
				cancel()
				<-lease.ch
				dropRef()
			})
		}, nil
	case <-ctx.Done():
		dropRef()
		return nil, nil, fmt.Errorf("delegation: waiting for a lease: %w", ctx.Err())
	}
}

// Subjects lists the people with a kept token, in no particular order.
func (s *MemoryStore) Subjects(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subjects := make([]string, 0, len(s.entries))
	for subject := range s.entries {
		subjects = append(subjects, subject)
	}
	return subjects, nil
}

// Close does nothing; there is no connection to end.
func (s *MemoryStore) Close() error { return nil }
