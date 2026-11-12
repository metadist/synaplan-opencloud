package delegation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/idp"
)

type memEntry struct {
	token string
	rev   uint64
}

// memStore is an in-process stand-in for the NATS bucket: one revision per
// person, and a write or delete that names a stale revision does not land.
type memStore struct {
	mu  sync.Mutex
	m   map[string]memEntry
	err error
}

func newMemStore() *memStore {
	return &memStore{m: map[string]memEntry{}}
}

func (s *memStore) Get(subject string) (string, uint64, bool, error) {
	if s.err != nil {
		return "", 0, false, s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.m[subject]
	if !ok {
		return "", 0, false, nil
	}
	return entry.token, entry.rev, true, nil
}

func (s *memStore) Write(subject, token string, match uint64) (uint64, bool, error) {
	if s.err != nil {
		return 0, false, s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.m[subject]
	if match == 0 {
		if ok {
			return 0, false, nil
		}
		s.m[subject] = memEntry{token: token, rev: 1}
		return 1, true, nil
	}
	if !ok || entry.rev != match {
		return 0, false, nil
	}
	entry.token = token
	entry.rev++
	s.m[subject] = entry
	return entry.rev, true, nil
}

func (s *memStore) Delete(subject string, match uint64) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.m[subject]
	if !ok {
		return true, nil
	}
	if entry.rev != match {
		return false, nil
	}
	delete(s.m, subject)
	return true, nil
}

func (s *memStore) Close() error { return nil }

func withStore(f *fakeIdP, store Store, failed *[]error) *Delegation {
	return New(Config{
		Provider:     idp.New(f.URL, idp.Endpoints{}, f.Client()),
		ClientID:     "synaplan-opencloud",
		ClientSecret: "secret",
		HTTPClient:   f.Client(),
		Store:        store,
		StoreFailed:  func(err error) { *failed = append(*failed, err) },
	})
}

func TestAConnectedPersonStaysConnectedAcrossARestart(t *testing.T) {
	idp := newIdP(t, "ada", false)
	store := newMemStore()
	var failed []error
	if err := connect(t, withStore(idp, store, &failed), "ada"); err != nil {
		t.Fatal(err)
	}

	restarted := withStore(idp, store, &failed)
	if !restarted.Connected("ada") {
		t.Fatal("the restart forgot the person")
	}
	if _, err := restarted.Token(context.Background(), "ada", "synaplan-app"); err != nil {
		t.Fatal(err)
	}
	if _, refreshed, _ := idp.state(); len(refreshed) != 1 || refreshed[0] != "offline-1" {
		t.Fatalf("refreshed with %v, want the kept offline-1", refreshed)
	}

	again := withStore(idp, store, &failed)
	if _, err := again.Token(context.Background(), "ada", "synaplan-app"); err != nil {
		t.Fatal(err)
	}
	if _, refreshed, _ := idp.state(); refreshed[len(refreshed)-1] != "offline-2" {
		t.Fatalf("refreshed with %v, want the rotated offline-2 kept", refreshed)
	}

	if err := again.Disconnect(context.Background(), "ada"); err != nil {
		t.Fatal(err)
	}
	if withStore(idp, store, &failed).Connected("ada") {
		t.Fatal("a disconnected person came back after a restart")
	}
	if len(failed) != 0 {
		t.Fatalf("store failed: %v", failed)
	}
}

func TestPeopleDoNotOverwriteEachOther(t *testing.T) {
	idp := newIdP(t, "ada", false)
	store := newMemStore()
	var failed []error
	first := withStore(idp, store, &failed)
	if err := connect(t, first, "ada"); err != nil {
		t.Fatal(err)
	}
	idp.mu.Lock()
	idp.codeSub = "bob"
	idp.mu.Unlock()
	if err := connect(t, first, "bob"); err != nil {
		t.Fatal(err)
	}

	second := withStore(idp, store, &failed)
	if !second.Connected("ada") || !second.Connected("bob") {
		t.Fatal("a second replica cannot see both people")
	}
	if err := second.Disconnect(context.Background(), "ada"); err != nil {
		t.Fatal(err)
	}
	if first.Connected("ada") || !first.Connected("bob") {
		t.Fatal("removing one person removed the other")
	}
	if len(failed) != 0 {
		t.Fatalf("store failed: %v", failed)
	}
}

func TestALostRevisionDoesNotDropTheOtherReplicasToken(t *testing.T) {
	idp := newIdP(t, "ada", false)
	store := &rotateOnDelete{memStore: newMemStore()}
	var failed []error
	d := withStore(idp, store, &failed)
	if err := connect(t, d, "ada"); err != nil {
		t.Fatal(err)
	}
	idp.mu.Lock()
	idp.dead = map[string]bool{"offline-1": true}
	idp.mu.Unlock()
	store.armed = true

	if _, err := d.Token(context.Background(), "ada", "synaplan-app"); err != nil {
		t.Fatal(err)
	}
	if _, refreshed, _ := idp.state(); len(refreshed) < 2 || refreshed[0] != "offline-1" || refreshed[1] != "offline-9" {
		t.Fatalf("refreshed with %v, want offline-1 refused and then offline-9", refreshed)
	}
	kept, _, ok, err := store.Get("ada")
	if err != nil || !ok || kept != "offline-2" {
		t.Fatalf("kept %q ok %v err %v, want the refresh of the other replica's token", kept, ok, err)
	}
	if !withStore(idp, store, &failed).Connected("ada") {
		t.Fatal("the lost revision disconnected the person")
	}
}

// rotateOnDelete is another replica replacing the token in the moment this
// one tries to forget a refused refresh.
type rotateOnDelete struct {
	*memStore
	armed bool
}

func (s *rotateOnDelete) Delete(subject string, match uint64) (bool, error) {
	if s.armed {
		s.armed = false
		if _, ok, err := s.Write(subject, "offline-9", match); err != nil || !ok {
			return false, errors.New("the other replica could not store its token")
		}
		return false, nil
	}
	return s.memStore.Delete(subject, match)
}

func TestALostWriteDoesNotReplaceTheWinningToken(t *testing.T) {
	idp := newIdP(t, "ada", false)
	store := &rejectReplace{memStore: newMemStore()}
	var failed []error
	d := withStore(idp, store, &failed)
	if err := connect(t, d, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Token(context.Background(), "ada", "synaplan-app"); err != nil {
		t.Fatal(err)
	}
	kept, _, ok, err := store.Get("ada")
	if err != nil || !ok || kept != "offline-1" {
		t.Fatalf("kept %q ok %v err %v, want the token that won", kept, ok, err)
	}
}

// rejectReplace loses every replace, as a replica does when its revision
// is no longer current.
type rejectReplace struct {
	*memStore
}

func (s *rejectReplace) Write(subject, token string, match uint64) (uint64, bool, error) {
	if match != 0 {
		return 0, false, nil
	}
	return s.memStore.Write(subject, token, match)
}

func TestAStoreThatFailsDoesNotPretendThePersonIsConnected(t *testing.T) {
	idp := newIdP(t, "ada", false)
	store := newMemStore()
	store.err = errors.New("nats down")
	var failed []error
	d := withStore(idp, store, &failed)
	err := connect(t, d, "ada")
	if !errors.Is(err, ErrUnavailable) || d.Connected("ada") || len(failed) == 0 {
		t.Fatalf("err %v, connected %v, failures %d", err, d.Connected("ada"), len(failed))
	}
}

func TestNewNATSNeedsAnAddress(t *testing.T) {
	if _, err := NewNATS(NATSConfig{}); err == nil {
		t.Fatal("NewNATS connected without an address")
	}
}

func TestNATSKeyIsASingleToken(t *testing.T) {
	key := natsKey("ada.bob@example")
	if key == "" || strings.ContainsAny(key, ".@= ") {
		t.Fatalf("key %q is not one NATS token", key)
	}
	if natsKey("ada") == natsKey("bob") {
		t.Fatal("different people share a key")
	}
}
