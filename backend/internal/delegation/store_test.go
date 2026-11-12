package delegation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/idp"
)

// failingStore is a store that cannot be reached.
type failingStore struct{ err error }

func (s failingStore) Get(context.Context, string) (string, uint64, bool, error) {
	return "", 0, false, s.err
}
func (s failingStore) Write(context.Context, string, string, uint64) (uint64, bool, error) {
	return 0, false, s.err
}
func (s failingStore) Delete(context.Context, string, uint64) (bool, error) { return false, s.err }
func (s failingStore) Lease(context.Context, string) (func(context.Context), error) {
	return nil, s.err
}
func (s failingStore) Close() error { return nil }

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
	store := NewMemoryStore()
	var failed []error
	if err := connect(t, withStore(idp, store, &failed), "ada"); err != nil {
		t.Fatal(err)
	}

	restarted := withStore(idp, store, &failed)
	if !restarted.Connected(context.Background(), "ada") {
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
	if withStore(idp, store, &failed).Connected(context.Background(), "ada") {
		t.Fatal("a disconnected person came back after a restart")
	}
	if len(failed) != 0 {
		t.Fatalf("store failed: %v", failed)
	}
}

func TestPeopleDoNotOverwriteEachOther(t *testing.T) {
	idp := newIdP(t, "ada", false)
	store := NewMemoryStore()
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
	if !second.Connected(context.Background(), "ada") || !second.Connected(context.Background(), "bob") {
		t.Fatal("a second replica cannot see both people")
	}
	if err := second.Disconnect(context.Background(), "ada"); err != nil {
		t.Fatal(err)
	}
	if first.Connected(context.Background(), "ada") || !first.Connected(context.Background(), "bob") {
		t.Fatal("removing one person removed the other")
	}
	if len(failed) != 0 {
		t.Fatalf("store failed: %v", failed)
	}
}

func TestALostRevisionDoesNotDropTheOtherReplicasToken(t *testing.T) {
	idp := newIdP(t, "ada", false)
	store := &rotateOnDelete{MemoryStore: NewMemoryStore()}
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
	kept, _, ok, err := store.Get(context.Background(), "ada")
	if err != nil || !ok || kept != "offline-2" {
		t.Fatalf("kept %q ok %v err %v, want the refresh of the other replica's token", kept, ok, err)
	}
	if !withStore(idp, store, &failed).Connected(context.Background(), "ada") {
		t.Fatal("the lost revision disconnected the person")
	}
}

// rotateOnDelete is another replica replacing the token in the moment this
// one tries to forget a refused refresh.
type rotateOnDelete struct {
	*MemoryStore
	armed bool
}

func (s *rotateOnDelete) Delete(ctx context.Context, subject string, match uint64) (bool, error) {
	if s.armed {
		s.armed = false
		if _, ok, err := s.Write(ctx, subject, "offline-9", match); err != nil || !ok {
			return false, errors.New("the other replica could not store its token")
		}
		return false, nil
	}
	return s.MemoryStore.Delete(ctx, subject, match)
}

func TestALostWriteDoesNotReplaceTheWinningToken(t *testing.T) {
	idp := newIdP(t, "ada", false)
	store := &rejectReplace{MemoryStore: NewMemoryStore()}
	var failed []error
	d := withStore(idp, store, &failed)
	if err := connect(t, d, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Token(context.Background(), "ada", "synaplan-app"); err != nil {
		t.Fatal(err)
	}
	kept, _, ok, err := store.Get(context.Background(), "ada")
	if err != nil || !ok || kept != "offline-1" {
		t.Fatalf("kept %q ok %v err %v, want the token that won", kept, ok, err)
	}
}

// rejectReplace loses every replace, as a replica does when its revision
// is no longer current.
type rejectReplace struct {
	*MemoryStore
}

func (s *rejectReplace) Write(ctx context.Context, subject, token string, match uint64) (uint64, bool, error) {
	if match != 0 {
		return 0, false, nil
	}
	return s.MemoryStore.Write(ctx, subject, token, match)
}

func TestAStoreThatFailsDoesNotPretendThePersonIsConnected(t *testing.T) {
	idp := newIdP(t, "ada", false)
	store := failingStore{err: errors.New("nats down")}
	var failed []error
	d := withStore(idp, store, &failed)
	err := connect(t, d, "ada")
	if !errors.Is(err, ErrUnavailable) || d.Connected(context.Background(), "ada") || len(failed) == 0 {
		t.Fatalf("err %v, connected %v, failures %d", err, d.Connected(context.Background(), "ada"), len(failed))
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

func TestARefreshForSomeoneElseIsNotKept(t *testing.T) {
	idp := newIdP(t, "ada", false)
	store := NewMemoryStore()
	var failed []error
	d := withStore(idp, store, &failed)
	if err := connect(t, d, "ada"); err != nil {
		t.Fatal(err)
	}
	idp.mu.Lock()
	idp.codeSub = "eve"
	idp.mu.Unlock()

	if _, err := d.Token(context.Background(), "ada", "synaplan-app"); err == nil {
		t.Fatal("a token for eve was used for ada")
	}
	if kept, _, _, _ := store.Get(context.Background(), "ada"); kept != "offline-1" {
		t.Fatalf("kept %q, want ada's own offline-1", kept)
	}
}

func TestMemoryStoreRevisions(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*MemoryStore) uint64
		write string
		match func(uint64) uint64
		want  bool
	}{
		{"create when absent", func(*MemoryStore) uint64 { return 0 }, "t1", func(uint64) uint64 { return 0 }, true},
		{"create when present", seed, "t2", func(uint64) uint64 { return 0 }, false},
		{"replace at the read revision", seed, "t2", func(r uint64) uint64 { return r }, true},
		{"replace at a stale revision", seed, "t2", func(r uint64) uint64 { return r + 1 }, false},
		{"replace when absent", func(*MemoryStore) uint64 { return 0 }, "t2", func(uint64) uint64 { return 1 }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewMemoryStore()
			rev := tc.setup(s)
			if _, ok, err := s.Write(context.Background(), "ada", tc.write, tc.match(rev)); err != nil || ok != tc.want {
				t.Fatalf("ok %v err %v, want %v", ok, err, tc.want)
			}
		})
	}

	s := NewMemoryStore()
	rev := seed(s)
	if ok, _ := s.Delete(context.Background(), "ada", rev+1); ok {
		t.Fatal("a stale revision deleted the token")
	}
	if ok, _ := s.Delete(context.Background(), "ada", rev); !ok {
		t.Fatal("the read revision did not delete the token")
	}
	if _, _, ok, _ := s.Get(context.Background(), "ada"); ok {
		t.Fatal("the token is still there")
	}
}

func seed(s *MemoryStore) uint64 {
	rev, _, _ := s.Write(context.Background(), "ada", "t1", 0)
	return rev
}

func TestReplicasRefreshingAtOnceDoNotEndTheSession(t *testing.T) {
	idp := newIdP(t, "ada", false)
	idp.singleUse = true
	store := NewMemoryStore()
	var failed []error
	if err := connect(t, withStore(idp, store, &failed), "ada"); err != nil {
		t.Fatal(err)
	}

	replicas := []*Delegation{withStore(idp, store, &failed), withStore(idp, store, &failed), withStore(idp, store, &failed)}
	errs := make(chan error, len(replicas))
	var wg sync.WaitGroup
	for _, d := range replicas {
		wg.Go(func() {
			_, err := d.Token(context.Background(), "ada", "synaplan-app")
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a replica failed: %v", err)
		}
	}
	idp.mu.Lock()
	ended := idp.refuse
	idp.mu.Unlock()
	if ended {
		t.Fatal("two replicas refreshed the same token and ended the session")
	}
}
