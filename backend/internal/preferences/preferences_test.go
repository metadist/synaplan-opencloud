package preferences

import (
	"context"
	"reflect"
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/natskv"
)

func TestMemoryStore(t *testing.T) {
	allowed := Preferences{ReadScope: ReadScope{Mode: ReadAllowed, Folders: []Folder{{ID: "s$p!f", Path: "Specs"}}}}
	tests := []struct {
		name string
		put  *Preferences
		want Preferences
	}{
		{name: "never saved", want: Default()},
		{name: "saved", put: &allowed, want: allowed},
		{name: "no folders", put: &Preferences{ReadScope: ReadScope{Mode: ReadAll}}, want: Preferences{ReadScope: ReadScope{Mode: ReadAll, Folders: []Folder{}}}},
		{name: "unknown mode", put: &Preferences{ReadScope: ReadScope{Mode: "everything"}}, want: Default()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewMemoryStore()
			ctx := context.Background()
			if tt.put != nil {
				if err := s.Put(ctx, "ada", *tt.put); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.Get(ctx, "ada")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			other, _ := s.Get(ctx, "eve")
			if !reflect.DeepEqual(other, Default()) {
				t.Fatalf("eve sees %+v", other)
			}
		})
	}
}

func TestMemoryStoreNeedsASubject(t *testing.T) {
	if err := NewMemoryStore().Put(context.Background(), "", Default()); err == nil {
		t.Fatal("stored a record without a subject")
	}
}

func TestValidMode(t *testing.T) {
	for mode, want := range map[Mode]bool{"watched": true, "allowed": true, "all": true, "": false, "All": false} {
		if got := ValidMode(mode); got != want {
			t.Errorf("ValidMode(%q) = %v", mode, got)
		}
	}
}

func TestNewNATSNeedsAnAddressAndABucket(t *testing.T) {
	cases := []struct {
		name     string
		cfg      natskv.Config
		bucket   string
		replicas int
	}{
		{"no address", natskv.Config{}, "synaplan-preferences", 1},
		{"no bucket", natskv.Config{Nodes: []string{"nats:9233"}}, "", 1},
		{"negative replicas", natskv.Config{Nodes: []string{"nats:9233"}}, "synaplan-preferences", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewNATS(tc.cfg, tc.bucket, tc.replicas); err == nil {
				t.Fatalf("NewNATS accepted %+v %q %d", tc.cfg, tc.bucket, tc.replicas)
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
	if _, other, err := s.Lease(context.Background(), "eve"); err != nil {
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
}

func TestKeyIsOneNATSToken(t *testing.T) {
	if got := key("ada@example.org"); got == "" || got != key("ada@example.org") || got == key("eve@example.org") {
		t.Fatalf("key %q", got)
	}
}
