package delegation

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestSettings(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		store func(t *testing.T) (Store, *MemoryStore)
	}{
		{"in memory", func(*testing.T) (Store, *MemoryStore) { m := NewMemoryStore(); return m, m }},
		{"sealed", func(t *testing.T) (Store, *MemoryStore) { m := NewMemoryStore(); return sealed(t, m, "secret"), m }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, inner := tc.store(t)
			d := New(Config{Store: store})

			value, rev, err := d.Setting(ctx, "read-scope", "ada")
			if err != nil || rev != 0 || value != nil {
				t.Fatalf("nothing kept: got %q at %d, %v", value, rev, err)
			}
			if ok, err := d.PutSetting(ctx, "read-scope", "ada", []byte(`{"mode":"allowed"}`), 0); err != nil || !ok {
				t.Fatalf("first write: %v %v", ok, err)
			}
			if ok, _ := d.PutSetting(ctx, "read-scope", "ada", []byte(`{"mode":"watched"}`), 0); ok {
				t.Fatal("a second create overwrote the kept setting")
			}
			value, rev, err = d.Setting(ctx, "read-scope", "ada")
			if err != nil || string(value) != `{"mode":"allowed"}` || rev == 0 {
				t.Fatalf("got %q at %d, %v", value, rev, err)
			}
			if ok, _ := d.PutSetting(ctx, "read-scope", "ada", []byte(`{}`), rev+7); ok {
				t.Fatal("a write at a stale revision went through")
			}
			if ok, err := d.PutSetting(ctx, "read-scope", "ada", []byte(`{"mode":"watched"}`), rev); err != nil || !ok {
				t.Fatalf("update: %v %v", ok, err)
			}
			if value, _, _ := d.Setting(ctx, "read-scope", "eve"); value != nil {
				t.Fatalf("eve got ada's setting %q", value)
			}

			subjects, err := store.Subjects(ctx)
			if err != nil || len(subjects) != 0 {
				t.Fatalf("a setting was listed as a person with a token: %v %v", subjects, err)
			}
			raw, _, _ := inner.GetSetting(ctx, "read-scope", "ada")
			if _, isSealed := store.(*SealedStore); isSealed && strings.Contains(string(raw), "watched") {
				t.Fatalf("the sealed store kept %q in the clear", raw)
			}
		})
	}
}

func TestASealedSettingIsBoundToItsNameAndPerson(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryStore()
	store := sealed(t, inner, "secret")
	if _, ok, err := store.PutSetting(ctx, "read-scope", "ada", []byte("ada's"), 0); err != nil || !ok {
		t.Fatal(ok, err)
	}
	raw, _, _ := inner.GetSetting(ctx, "read-scope", "ada")
	for _, moved := range [][2]string{{"read-scope", "eve"}, {"other", "ada"}} {
		if _, ok, err := inner.PutSetting(ctx, moved[0], moved[1], raw, 0); err != nil || !ok {
			t.Fatal(ok, err)
		}
		if _, _, err := store.GetSetting(ctx, moved[0], moved[1]); !errors.Is(err, ErrUnreadable) {
			t.Fatalf("%v: err %v, want ErrUnreadable", moved, err)
		}
	}
}

func TestSettingNames(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"", "lease", "Read", "a.b", "has space"} {
		if _, _, err := NewMemoryStore().GetSetting(ctx, name, "ada"); err == nil {
			t.Fatalf("%q was accepted as a setting name", name)
		}
	}
}

func TestSettingsNeedAStoreThatKeepsThem(t *testing.T) {
	ctx := context.Background()
	d := New(Config{Store: failingStore{err: errors.New("down")}})
	if _, _, err := d.Setting(ctx, "read-scope", "ada"); !errors.Is(err, errNoSettings) {
		t.Fatalf("err %v, want errNoSettings", err)
	}
	if err := d.WithSettingLease(ctx, "read-scope", "ada", func(context.Context) error { return nil }); !errors.Is(err, errNoSettings) {
		t.Fatalf("err %v, want errNoSettings", err)
	}
	var nilD *Delegation
	if _, err := nilD.PutSetting(ctx, "read-scope", "ada", nil, 0); !errors.Is(err, errNoSettings) {
		t.Fatalf("err %v, want errNoSettings", err)
	}
}

func TestASettingLeaseIsNotTheTokenLease(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	d := New(Config{Store: store})
	var order []string
	err := d.WithSettingLease(ctx, "read-scope", "ada", func(held context.Context) error {
		// The person's token lease stays free while their setting is changed.
		_, release, err := store.Lease(held, "ada")
		if err != nil {
			return err
		}
		release()
		order = append(order, "changed")
		return nil
	})
	if err != nil || !slices.Equal(order, []string{"changed"}) {
		t.Fatalf("err %v, order %v", err, order)
	}
}
