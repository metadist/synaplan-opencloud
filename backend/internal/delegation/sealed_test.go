package delegation

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func sealedWith(t *testing.T, inner Store, secrets ...string) (*SealedStore, *[]string) {
	t.Helper()
	keys, err := NewLocalKeys(secrets...)
	if err != nil {
		t.Fatal(err)
	}
	var unreadable []string
	s := NewSealedStore(inner, keys)
	s.Unreadable = func(subject string, _ error) { unreadable = append(unreadable, subject) }
	return s, &unreadable
}

func TestSealedStore(t *testing.T) {
	cases := []struct {
		name string
		// before changes what the store holds for ada, sealed with "one",
		// before it is read with readWith.
		before     func(inner *MemoryStore)
		readWith   []string
		want       string
		unreadable bool
	}{
		{name: "round trip", readWith: []string{"one"}, want: "offline-1"},
		{name: "after a rotation the previous secret still reads", readWith: []string{"two", "one"}, want: "offline-1"},
		{name: "a dropped secret does not read", readWith: []string{"two"}, unreadable: true},
		{
			name: "a value copied to another person does not read",
			before: func(inner *MemoryStore) {
				eve, _, _, _ := inner.Get("eve")
				_, rev, _, _ := inner.Get("ada")
				_, _, _ = inner.Write("ada", eve, rev)
			},
			readWith:   []string{"one"},
			unreadable: true,
		},
		{
			name: "a value that was never sealed does not read",
			before: func(inner *MemoryStore) {
				_, rev, _, _ := inner.Get("ada")
				_, _, _ = inner.Write("ada", "offline-plain", rev)
			},
			readWith:   []string{"one"},
			unreadable: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inner := NewMemoryStore()
			writer, _ := sealedWith(t, inner, "one")
			if _, ok, err := writer.Write("ada", "offline-1", 0); err != nil || !ok {
				t.Fatalf("write: ok %v err %v", ok, err)
			}
			if _, ok, err := writer.Write("eve", "offline-eve", 0); err != nil || !ok {
				t.Fatalf("write: ok %v err %v", ok, err)
			}
			if tc.before != nil {
				tc.before(inner)
			}
			reader, unreadable := sealedWith(t, inner, tc.readWith...)
			got, _, ok, err := reader.Get("ada")
			if err != nil || !ok {
				t.Fatalf("get: ok %v err %v", ok, err)
			}
			if got != tc.want || (len(*unreadable) == 1) != tc.unreadable {
				t.Fatalf("got %q, unreadable %v", got, *unreadable)
			}
		})
	}
}

func TestSealedStoreKeepsNothingReadable(t *testing.T) {
	inner := NewMemoryStore()
	s, _ := sealedWith(t, inner, "one")
	if _, _, err := s.Write("ada", "offline-1", 0); err != nil {
		t.Fatal(err)
	}
	raw, _, _, _ := inner.Get("ada")
	if strings.Contains(raw, "offline-1") || !strings.HasPrefix(raw, "v1.") {
		t.Fatalf("stored %q", raw)
	}
}

func TestSealedStoreWritesWithTheCurrentKey(t *testing.T) {
	inner := NewMemoryStore()
	old, _ := sealedWith(t, inner, "one")
	rev, _, _ := old.Write("ada", "offline-1", 0)
	before, _, _, _ := inner.Get("ada")

	rotated, _ := sealedWith(t, inner, "two", "one")
	if _, ok, err := rotated.Write("ada", "offline-2", rev); err != nil || !ok {
		t.Fatalf("ok %v err %v", ok, err)
	}
	after, _, _, _ := inner.Get("ada")
	if strings.Split(before, ".")[1] == strings.Split(after, ".")[1] {
		t.Fatal("the rewrite kept the previous key")
	}
	if got, _, _, _ := mustSealed(t, inner, "two").Get("ada"); got != "offline-2" {
		t.Fatalf("the current key alone reads %q", got)
	}
}

func TestNewLocalKeysNeedsASecret(t *testing.T) {
	if _, err := NewLocalKeys("", ""); err == nil {
		t.Fatal("keys without a secret")
	}
}

func TestAnUnreadableTokenMeansAllowAgain(t *testing.T) {
	idp := newIdP(t, "ada", false)
	inner := NewMemoryStore()
	var failed []error
	if err := connect(t, withStore(idp, mustSealed(t, inner, "one"), &failed), "ada"); err != nil {
		t.Fatal(err)
	}

	d := withStore(idp, mustSealed(t, inner, "two"), &failed)
	if d.Connected("ada") {
		t.Fatal("an unreadable token counts as connected")
	}
	if _, err := d.Token(context.Background(), "ada", "synaplan-app"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err %v, want ErrNotConnected", err)
	}
	if err := connect(t, d, "ada"); err != nil {
		t.Fatal(err)
	}
	if !d.Connected("ada") {
		t.Fatal("allowing again did not connect")
	}
}

func mustSealed(t *testing.T, inner Store, secrets ...string) *SealedStore {
	s, _ := sealedWith(t, inner, secrets...)
	return s
}
