package delegation

import (
	"errors"
	"sync"
)

// MemoryStore keeps the offline tokens in this process with the same
// revisions as the NATS bucket. A restart forgets them, and another replica
// never sees them, so it only fits a single instance that accepts people
// allowing again after a restart, and tests.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]memoryEntry
}

type memoryEntry struct {
	token    string
	revision uint64
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: map[string]memoryEntry{}}
}

// Get reads the person's token.
func (s *MemoryStore) Get(subject string) (string, uint64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[subject]
	return entry.token, entry.revision, ok, nil
}

// Write stores the token when match is still current.
func (s *MemoryStore) Write(subject, token string, match uint64) (uint64, bool, error) {
	if token == "" {
		return 0, false, errors.New("delegation: refusing to store an empty offline token")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[subject]
	switch {
	case match == 0 && ok, match != 0 && (!ok || entry.revision != match):
		return 0, false, nil
	}
	entry.token = token
	entry.revision++
	s.entries[subject] = entry
	return entry.revision, true, nil
}

// Delete removes the token when match is still current.
func (s *MemoryStore) Delete(subject string, match uint64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[subject]
	if !ok {
		return true, nil
	}
	if entry.revision != match {
		return false, nil
	}
	delete(s.entries, subject)
	return true, nil
}

// Close does nothing; there is no connection to end.
func (s *MemoryStore) Close() error { return nil }
