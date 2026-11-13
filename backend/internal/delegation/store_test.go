package delegation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/idp"
	"github.com/nats-io/nats.go/jetstream"
)

// failingStore is a store that cannot be reached.
type failingStore struct{ err error }

func (s failingStore) Get(context.Context, string) (Entry, bool, error) { return Entry{}, false, s.err }
func (s failingStore) Write(context.Context, string, string, uint64) (uint64, bool, error) {
	return 0, false, s.err
}
func (s failingStore) Delete(context.Context, string, uint64) (bool, error) { return false, s.err }
func (s failingStore) Lease(context.Context, string) (context.Context, func(), error) {
	return nil, nil, s.err
}
func (s failingStore) Close() error { return nil }

// rotateOnDelete is another replica replacing the token in the moment this
// one tries to forget a refused refresh.
type rotateOnDelete struct {
	*MemoryStore
	mu    sync.Mutex
	armed bool
}

func (s *rotateOnDelete) Delete(ctx context.Context, subject string, match uint64) (bool, error) {
	s.mu.Lock()
	armed := s.armed
	s.armed = false
	s.mu.Unlock()
	if armed {
		if _, ok, err := s.Write(ctx, subject, "offline-9", match); err != nil || !ok {
			return false, errors.New("the other replica could not store its token")
		}
		return false, nil
	}
	return s.MemoryStore.Delete(ctx, subject, match)
}

// rejectReplace loses every replace, as a replica does when its revision
// is no longer current.
type rejectReplace struct{ *MemoryStore }

func (s rejectReplace) Write(ctx context.Context, subject, token string, match uint64) (uint64, bool, error) {
	if match != 0 {
		return 0, false, nil
	}
	return s.MemoryStore.Write(ctx, subject, token, match)
}

// failures collects what StoreFailed hears, from any goroutine.
type failures struct {
	mu   sync.Mutex
	errs []error
}

func (f *failures) add(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs = append(f.errs, err)
}

func (f *failures) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.errs)
}

func withStore(f *fakeIdP, store Store, failed *failures) *Delegation {
	return New(Config{
		Provider:     idp.New(f.URL, idp.Endpoints{}, f.Client()),
		ClientID:     "synaplan-opencloud",
		ClientSecret: "secret",
		HTTPClient:   f.Client(),
		Store:        store,
		StoreFailed:  failed.add,
	})
}

func token(d *Delegation) error {
	_, err := d.Token(context.Background(), "ada", "synaplan-app")
	return err
}

func storedFor(t *testing.T, store Store) string {
	t.Helper()
	entry, _, err := store.Get(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	return entry.Token
}

// Each scenario connects ada through one replica, then acts through
// replicas that share the store, as replicas or a restarted process do.
func TestReplicasShareTheStore(t *testing.T) {
	cases := []struct {
		name  string
		store func() Store
		run   func(t *testing.T, f *fakeIdP, store Store, replica func() *Delegation)
	}{
		{
			name:  "a connected person stays connected across a restart",
			store: func() Store { return NewMemoryStore() },
			run: func(t *testing.T, f *fakeIdP, store Store, replica func() *Delegation) {
				restarted := replica()
				if !connected(t, restarted, "ada") {
					t.Fatal("the restart forgot the person")
				}
				if err := token(restarted); err != nil {
					t.Fatal(err)
				}
				if err := token(replica()); err != nil {
					t.Fatal(err)
				}
				if _, refreshed, _ := f.state(); len(refreshed) != 2 || refreshed[0] != "offline-1" || refreshed[1] != "offline-2" {
					t.Fatalf("refreshed with %v, want the kept offline-1, then the rotated offline-2", refreshed)
				}
				if err := replica().Disconnect(context.Background(), "ada"); err != nil {
					t.Fatal(err)
				}
				if connected(t, replica(), "ada") {
					t.Fatal("a disconnected person came back after a restart")
				}
			},
		},
		{
			name:  "people do not overwrite each other",
			store: func() Store { return NewMemoryStore() },
			run: func(t *testing.T, f *fakeIdP, store Store, replica func() *Delegation) {
				first := replica()
				f.mu.Lock()
				f.codeSub = "bob"
				f.mu.Unlock()
				if err := connect(t, first, "bob"); err != nil {
					t.Fatal(err)
				}
				second := replica()
				if !connected(t, second, "ada") || !connected(t, second, "bob") {
					t.Fatal("a second replica cannot see both people")
				}
				if err := second.Disconnect(context.Background(), "ada"); err != nil {
					t.Fatal(err)
				}
				if connected(t, first, "ada") || !connected(t, first, "bob") {
					t.Fatal("removing one person removed the other")
				}
			},
		},
		{
			name:  "a lost revision does not drop the other replica's token",
			store: func() Store { return &rotateOnDelete{MemoryStore: NewMemoryStore()} },
			run: func(t *testing.T, f *fakeIdP, store Store, replica func() *Delegation) {
				f.mu.Lock()
				f.dead = map[string]bool{"offline-1": true}
				f.mu.Unlock()
				r := store.(*rotateOnDelete)
				r.mu.Lock()
				r.armed = true
				r.mu.Unlock()
				if err := token(replica()); err != nil {
					t.Fatal(err)
				}
				if _, refreshed, _ := f.state(); len(refreshed) < 2 || refreshed[0] != "offline-1" || refreshed[1] != "offline-9" {
					t.Fatalf("refreshed with %v, want offline-1 refused and then offline-9", refreshed)
				}
				if got := storedFor(t, store); got != "offline-2" {
					t.Fatalf("kept %q, want the refresh of the other replica's token", got)
				}
			},
		},
		{
			name:  "a lost write does not replace the winning token",
			store: func() Store { return rejectReplace{NewMemoryStore()} },
			run: func(t *testing.T, f *fakeIdP, store Store, replica func() *Delegation) {
				if err := token(replica()); err != nil {
					t.Fatal(err)
				}
				if got := storedFor(t, store); got != "offline-1" {
					t.Fatalf("kept %q, want the token that won", got)
				}
			},
		},
		{
			name:  "a refresh for someone else is not kept",
			store: func() Store { return NewMemoryStore() },
			run: func(t *testing.T, f *fakeIdP, store Store, replica func() *Delegation) {
				f.mu.Lock()
				f.codeSub = "eve"
				f.mu.Unlock()
				if err := token(replica()); err == nil {
					t.Fatal("a token for eve was used for ada")
				}
				if got := storedFor(t, store); got != "offline-1" {
					t.Fatalf("kept %q, want ada's own offline-1", got)
				}
			},
		},
		{
			name:  "replicas refreshing at once do not end the session",
			store: func() Store { return NewMemoryStore() },
			run: func(t *testing.T, f *fakeIdP, store Store, replica func() *Delegation) {
				f.mu.Lock()
				f.singleUse = true
				f.mu.Unlock()
				// More replicas than storeAttempts proves waiting replicas use the
				// revision read under the lease instead of thrashing on stale reads.
				replicas := make([]*Delegation, storeAttempts+3)
				for i := range replicas {
					replicas[i] = replica()
				}
				errs := make([]error, len(replicas))
				var wg sync.WaitGroup
				for i, d := range replicas {
					wg.Go(func() { errs[i] = token(d) })
				}
				wg.Wait()
				if err := errors.Join(errs...); err != nil {
					t.Fatalf("a replica failed: %v", err)
				}
				f.mu.Lock()
				ended := f.refuse
				f.mu.Unlock()
				if ended {
					t.Fatal("two replicas refreshed the same token and ended the session")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newIdP(t, "ada", false)
			store := tc.store()
			var failed failures
			replica := func() *Delegation { return withStore(f, store, &failed) }
			if err := connect(t, replica(), "ada"); err != nil {
				t.Fatal(err)
			}
			tc.run(t, f, store, replica)
			if n := failed.count(); n != 0 {
				t.Fatalf("the store failed %d times", n)
			}
		})
	}
}

func TestAStoreThatFailsIsAnOutage(t *testing.T) {
	f := newIdP(t, "ada", false)
	var failed failures
	d := withStore(f, failingStore{err: errors.New("nats down")}, &failed)
	err := connect(t, d, "ada")
	ok, connErr := d.Connected(context.Background(), "ada")
	if !errors.Is(err, ErrUnavailable) || !errors.Is(connErr, ErrUnavailable) || ok || failed.count() == 0 {
		t.Fatalf("connect %v, connected %v %v, failures %d", err, ok, connErr, failed.count())
	}

	// A caller that cancelled its own context is not a store outage in the log.
	var cancelledFailures failures
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelledDel := withStore(f, NewMemoryStore(), &cancelledFailures)
	_, _ = cancelledDel.Token(cancelledCtx, "ada", "synaplan-app")
	if n := cancelledFailures.count(); n != 0 {
		t.Fatalf("caller cancellation reported %d store failures", n)
	}
}

func TestMemoryStoreRevisions(t *testing.T) {
	ctx := context.Background()
	seed := func(s *MemoryStore) uint64 {
		rev, ok, err := s.Write(ctx, "ada", "t1", 0)
		if err != nil || !ok {
			t.Fatalf("seed: ok %v err %v", ok, err)
		}
		return rev
	}
	none := func(*MemoryStore) uint64 { return 0 }
	cases := []struct {
		name  string
		setup func(*MemoryStore) uint64
		match func(uint64) uint64
		want  bool
	}{
		{"create when absent", none, func(uint64) uint64 { return 0 }, true},
		{"create when present", seed, func(uint64) uint64 { return 0 }, false},
		{"replace at the read revision", seed, func(r uint64) uint64 { return r }, true},
		{"replace at a stale revision", seed, func(r uint64) uint64 { return r + 1 }, false},
		{"replace when absent", none, func(uint64) uint64 { return 1 }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewMemoryStore()
			rev := tc.setup(s)
			if _, ok, err := s.Write(ctx, "ada", "t2", tc.match(rev)); err != nil || ok != tc.want {
				t.Fatalf("ok %v err %v, want %v", ok, err, tc.want)
			}
		})
	}

	s := NewMemoryStore()
	rev := seed(s)
	deletes := []struct {
		name  string
		match uint64
		want  bool
	}{
		{"a stale revision does not delete", rev + 1, false},
		{"the read revision deletes", rev, true},
		{"a missing token counts as deleted", rev, true},
	}
	for _, tc := range deletes {
		if ok, err := s.Delete(ctx, "ada", tc.match); err != nil || ok != tc.want {
			t.Fatalf("%s: ok %v err %v", tc.name, ok, err)
		}
	}
	if again := seed(s); again <= rev {
		t.Fatalf("revision %d after recreating, want more than %d", again, rev)
	}
}

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
			if got := keyExists(fmt.Errorf("create: %w", tc.err)); got != tc.want {
				t.Fatalf("keyExists = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMemoryStoreLease(t *testing.T) {
	s := NewMemoryStore()
	_, release, err := s.Lease(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	waiting, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.Lease(waiting, "ada"); err == nil {
		t.Fatal("a second holder got the lease")
	}
	if _, other, err := s.Lease(context.Background(), "bob"); err != nil {
		t.Fatal("another person's lease waited:", err)
	} else {
		other()
	}
	release()
	if _, again, err := s.Lease(context.Background(), "ada"); err != nil {
		t.Fatal("a released lease was not free:", err)
	} else {
		again()
	}
	if len(s.leases) != 0 {
		t.Fatalf("released leases left %d entries behind", len(s.leases))
	}
}

func TestNewNATSNeedsAnAddressAndABucket(t *testing.T) {
	for _, cfg := range []NATSConfig{
		{},
		{Nodes: []string{"nats://127.0.0.1:1"}},
		{Nodes: []string{"  ", ""}, Bucket: "synaplan-delegation"},
		{Nodes: []string{"nats://127.0.0.1:1"}, Bucket: "   "},
		{Nodes: []string{"nats://127.0.0.1:1"}, Bucket: "synaplan-delegation", Replicas: -1},
	} {
		if _, err := NewNATS(cfg); err == nil {
			t.Fatalf("NewNATS accepted %+v", cfg)
		}
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
