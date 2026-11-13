package synced

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestKeyRoundTrips(t *testing.T) {
	cases := []struct {
		name, subject, resourceID string
	}{
		{"plain", "ada", "space$id!opaque"},
		{"characters NATS rejects", "ada@example.org", "a.b c*>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := Key(tc.subject, tc.resourceID)
			if strings.Count(key, ".") != 1 || strings.ContainsAny(key, " *>@$!") {
				t.Fatalf("key %q is not one NATS key with one separator", key)
			}
			value, _ := json.Marshal(File{Etag: "e1", GroupKey: "DOCS"})
			got, ok := decode(key, value)
			if !ok {
				t.Fatal("key did not decode")
			}
			want := File{Subject: tc.subject, ResourceID: tc.resourceID, Etag: "e1", GroupKey: "DOCS"}
			if got != want {
				t.Fatalf("decoded %+v, want %+v", got, want)
			}
		})
	}
}

func TestDecodeRefusesForeignKeys(t *testing.T) {
	for _, key := range []string{"lease.x.y", "nodot", "!!!.MFRGG"} {
		if _, ok := decode(key, []byte(`{}`)); ok {
			t.Fatalf("decoded %q", key)
		}
	}
}

func TestMemoryStore(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		run  func(t *testing.T, s *MemoryStore)
		want []string
	}{
		{
			name: "put lists the file for its person only",
			run: func(t *testing.T, s *MemoryStore) {
				must(t, s.Put(ctx, File{Subject: "ada", ResourceID: "r1", Etag: "e1"}))
				must(t, s.Put(ctx, File{Subject: "eve", ResourceID: "r2", Etag: "e2"}))
			},
			want: []string{"r1@e1"},
		},
		{
			name: "update with the revision that was read moves the version",
			run: func(t *testing.T, s *MemoryStore) {
				must(t, s.Put(ctx, File{Subject: "ada", ResourceID: "r1", Etag: "e1"}))
				file := only(t, s)
				file.Etag = "e2"
				if ok, err := s.Update(ctx, file); err != nil || !ok {
					t.Fatalf("update: %v %v", ok, err)
				}
			},
			want: []string{"r1@e2"},
		},
		{
			name: "update after another write leaves the newer one",
			run: func(t *testing.T, s *MemoryStore) {
				must(t, s.Put(ctx, File{Subject: "ada", ResourceID: "r1", Etag: "e1"}))
				stale := only(t, s)
				must(t, s.Put(ctx, File{Subject: "ada", ResourceID: "r1", Etag: "e3"}))
				stale.Etag = "e2"
				if ok, err := s.Update(ctx, stale); err != nil || ok {
					t.Fatalf("update: %v %v", ok, err)
				}
			},
			want: []string{"r1@e3"},
		},
		{
			name: "update of a forgotten file does not bring it back",
			run: func(t *testing.T, s *MemoryStore) {
				must(t, s.Put(ctx, File{Subject: "ada", ResourceID: "r1", Etag: "e1"}))
				file := only(t, s)
				must(t, s.Delete(ctx, "ada", "r1", 0))
				if ok, _ := s.Update(ctx, file); ok {
					t.Fatal("updated a forgotten file")
				}
			},
			want: []string{},
		},
		{
			name: "delete with an old revision keeps the newer file",
			run: func(t *testing.T, s *MemoryStore) {
				must(t, s.Put(ctx, File{Subject: "ada", ResourceID: "r1", Etag: "e1"}))
				old := only(t, s)
				must(t, s.Put(ctx, File{Subject: "ada", ResourceID: "r1", Etag: "e2"}))
				must(t, s.Delete(ctx, "ada", "r1", old.Revision))
			},
			want: []string{"r1@e2"},
		},
		{
			name: "deleting a file that is not kept is fine",
			run: func(t *testing.T, s *MemoryStore) {
				must(t, s.Delete(ctx, "ada", "nope", 0))
			},
			want: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewMemoryStore()
			tc.run(t, s)
			files, err := s.Files(ctx, "ada")
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(files))
			for _, f := range files {
				got = append(got, f.ResourceID+"@"+f.Etag)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("kept %v, want %v", got, tc.want)
			}
		})
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func only(t *testing.T, s *MemoryStore) File {
	t.Helper()
	files, err := s.Files(context.Background(), "ada")
	if err != nil || len(files) != 1 {
		t.Fatalf("files %v %v", files, err)
	}
	return files[0]
}
