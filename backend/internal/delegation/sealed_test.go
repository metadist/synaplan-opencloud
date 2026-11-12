package delegation

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func sealed(t *testing.T, inner Store, secrets ...string) *SealedStore {
	t.Helper()
	keys, err := NewLocalKeys(secrets...)
	if err != nil {
		t.Fatal(err)
	}
	return NewSealedStore(inner, keys)
}

// outageKeys is a key wrapper on the network that cannot be reached.
type outageKeys struct{}

func (outageKeys) Wrap(context.Context, []byte, []byte) (string, []byte, error) {
	return "", nil, errors.New("kms timeout")
}
func (outageKeys) Unwrap(context.Context, string, []byte, []byte) ([]byte, bool, error) {
	return nil, false, errors.New("kms timeout")
}

func keyID(t *testing.T, inner Store) string {
	t.Helper()
	return strings.Split(storedFor(t, inner), ".")[1]
}

func TestSealedStore(t *testing.T) {
	ctx := context.Background()
	with := func(secrets ...string) func(*testing.T, Store) *SealedStore {
		return func(t *testing.T, inner Store) *SealedStore { return sealed(t, inner, secrets...) }
	}
	cases := []struct {
		name string
		// before changes what inner holds for ada, sealed with "one",
		// before it is read through read.
		before     func(inner *MemoryStore)
		read       func(*testing.T, Store) *SealedStore
		want       string
		unreadable bool
		outage     bool
	}{
		{name: "round trip", read: with("one"), want: "offline-1"},
		{name: "after a rotation the previous secret still reads", read: with("two", "one"), want: "offline-1"},
		{name: "a dropped secret does not read", read: with("two"), unreadable: true},
		{
			name: "a value copied to another person does not read",
			before: func(inner *MemoryStore) {
				eve, _, _ := inner.Get(ctx, "eve")
				ada, _, _ := inner.Get(ctx, "ada")
				_, _, _ = inner.Write(ctx, "ada", eve.Token, ada.Revision)
			},
			read:       with("one"),
			unreadable: true,
		},
		{
			name: "a value that was never sealed does not read",
			before: func(inner *MemoryStore) {
				ada, _, _ := inner.Get(ctx, "ada")
				_, _, _ = inner.Write(ctx, "ada", "offline-plain", ada.Revision)
			},
			read:       with("one"),
			unreadable: true,
		},
		{
			name:   "a key wrapper that cannot be reached is an outage, not an unreadable value",
			read:   func(_ *testing.T, inner Store) *SealedStore { return NewSealedStore(inner, outageKeys{}) },
			outage: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inner := NewMemoryStore()
			writer := sealed(t, inner, "one")
			for sub, tok := range map[string]string{"ada": "offline-1", "eve": "offline-eve"} {
				if _, ok, err := writer.Write(ctx, sub, tok, 0); err != nil || !ok {
					t.Fatalf("write: ok %v err %v", ok, err)
				}
			}
			if tc.before != nil {
				tc.before(inner)
			}
			got, ok, err := tc.read(t, inner).Get(ctx, "ada")
			switch {
			case tc.outage:
				if err == nil || errors.Is(err, ErrUnreadable) {
					t.Fatalf("err %v, want an outage", err)
				}
			case tc.unreadable:
				if !errors.Is(err, ErrUnreadable) || !ok || got.Revision == 0 {
					t.Fatalf("got %+v ok %v err %v, want unreadable with its revision", got, ok, err)
				}
			default:
				if err != nil || !ok || got.Token != tc.want {
					t.Fatalf("got %+v ok %v err %v", got, ok, err)
				}
			}
		})
	}
}

func TestSealedStoreKeepsNothingReadable(t *testing.T) {
	inner := NewMemoryStore()
	if _, _, err := sealed(t, inner, "one").Write(context.Background(), "ada", "offline-1", 0); err != nil {
		t.Fatal(err)
	}
	raw := storedFor(t, inner)
	if strings.Contains(raw, "offline-1") || !strings.HasPrefix(raw, "v1.") {
		t.Fatalf("stored %q", raw)
	}
}

func TestSealedStoreAsksToRewrapUnderAPreviousKey(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryStore()
	if _, _, err := sealed(t, inner, "one").Write(ctx, "ada", "offline-1", 0); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		secrets []string
		rewrap  bool
	}{
		{"current key", []string{"one"}, false},
		{"previous key", []string{"two", "one"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := storedFor(t, inner)
			got, _, err := sealed(t, inner, tc.secrets...).Get(ctx, "ada")
			if err != nil || got.Token != "offline-1" || got.Rewrap != tc.rewrap {
				t.Fatalf("got %+v err %v, want rewrap %v", got, err, tc.rewrap)
			}
			if storedFor(t, inner) != before {
				t.Fatal("a read wrote to the store")
			}
		})
	}
}

// A token the identity provider does not rotate is written again under the
// current key while its lease is held, so the previous secret can go.
func TestARefreshMovesTheTokenToTheCurrentKey(t *testing.T) {
	f := newIdP(t, "ada", false)
	inner := NewMemoryStore()
	var failed failures
	if err := connect(t, withStore(f, sealed(t, inner, "one"), &failed), "ada"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.keepRefresh = true
	f.mu.Unlock()
	before := keyID(t, inner)

	if err := token(withStore(f, sealed(t, inner, "two", "one"), &failed)); err != nil {
		t.Fatal(err)
	}
	if keyID(t, inner) == before {
		t.Fatal("the token is still sealed with the previous key")
	}
	if !connected(t, withStore(f, sealed(t, inner, "two"), &failed), "ada") {
		t.Fatal("the current key alone does not read the token")
	}
}

func TestNewLocalKeysNeedsASecret(t *testing.T) {
	if _, err := NewLocalKeys("", ""); err == nil {
		t.Fatal("keys without a secret")
	}
}

// An unreadable token is not connected on this replica and is replaced by
// allowing again, but never deleted for that alone.
func TestAnUnreadableTokenMeansAllowAgain(t *testing.T) {
	f := newIdP(t, "ada", false)
	inner := NewMemoryStore()
	var failed failures
	if err := connect(t, withStore(f, sealed(t, inner, "one"), &failed), "ada"); err != nil {
		t.Fatal(err)
	}

	d := withStore(f, sealed(t, inner, "two"), &failed)
	if connected(t, d, "ada") {
		t.Fatal("an unreadable token counts as connected")
	}
	if err := token(d); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err %v, want ErrNotConnected", err)
	}
	if !connected(t, withStore(f, sealed(t, inner, "two", "one"), &failed), "ada") {
		t.Fatal("a replica without the key deleted the token; a rotation rolling through the replicas would lose the person")
	}
	if err := connect(t, d, "ada"); err != nil {
		t.Fatal(err)
	}
	if !connected(t, d, "ada") {
		t.Fatal("allowing again did not connect")
	}
	if _, refreshed, revoked := f.state(); len(refreshed) != 0 || len(revoked) != 0 {
		t.Fatalf("refreshed %v, revoked %v: an unreadable token must not reach the identity provider", refreshed, revoked)
	}
}
