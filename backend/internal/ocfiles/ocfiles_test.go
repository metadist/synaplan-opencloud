package ocfiles

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const fileItem = `{"id":"s!a","name":"a.txt","eTag":"\"e1\"","size":5,"file":{"mimeType":"text/plain"},"parentReference":{"driveId":"s","path":"/Projects"}}`

const folderItem = `{"id":"s!f","name":"Projects","eTag":"\"e0\"","folder":{},"parentReference":{"driveId":"s","path":"/"}}`

// fakeCloud answers like OpenCloud (Graph and WebDAV) for the file s!a,
// the folder s!f and the personal space home. Paths in taken already exist; created collects
// the folders and files written.
func fakeCloud(t *testing.T, taken map[string]bool, created *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer person" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/graph/v1.0/drives/s/items/s!a":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, fileItem)
		case r.Method == http.MethodGet && r.URL.Path == "/graph/v1.0/drives/s/items/s!f":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, folderItem)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/graph/v1.0/drives/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/dav/spaces/s!a":
			_, _ = io.WriteString(w, "hello")
		case r.Method == http.MethodGet && r.URL.Path == "/graph/v1.0/me/drive":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"home","name":"Personal","driveType":"personal"}`)
		case r.Method == "MKCOL":
			if taken[r.URL.Path] {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			*created = append(*created, r.URL.Path)
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPut:
			if r.Header.Get("If-None-Match") != "*" {
				t.Errorf("PUT %s without If-None-Match: *", r.URL.Path)
			}
			if taken[r.URL.Path] {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			*created = append(*created, r.URL.Path)
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func client(url string) *Client {
	return New(url, http.DefaultClient, func(context.Context) (string, error) { return "person", nil })
}

func TestOpenReturnsTheFileWithItsPathInsideTheSpace(t *testing.T) {
	srv := fakeCloud(t, nil, &[]string{})
	file, err := client(srv.URL).Open(context.Background(), "s!a")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Body.Close() }()
	body, _ := io.ReadAll(file.Body)
	got := *file
	got.Body = nil
	want := File{Name: "a.txt", MimeType: "text/plain", Size: 5, Etag: `"e1"`, Path: "Projects/a.txt"}
	if got != want || string(body) != "hello" {
		t.Fatalf("file = %+v, body = %q", got, body)
	}
}

func TestStatRefuses(t *testing.T) {
	cases := []struct {
		name string
		id   string
		want func(error) bool
	}{
		{"a missing resource as not found", "s!gone", func(err error) bool { return errors.Is(err, ErrNotFound) }},
		{"a space root", "s", func(err error) bool { return err != nil && strings.Contains(err.Error(), "not a file") }},
		{"a folder", "s!f", func(err error) bool { return err != nil && strings.Contains(err.Error(), "not a file") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := fakeCloud(t, nil, &[]string{})
			if _, err := client(srv.URL).Stat(context.Background(), tc.id); !tc.want(err) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestSaveWritesIntoTheKindFolderUnderAFreeName(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		taken    map[string]bool
		want     string
		created  []string
	}{
		{
			name:     "new folders",
			filename: "notes.md",
			want:     "Synaplan/Documents/notes.md",
			created:  []string{"/dav/spaces/home/Synaplan", "/dav/spaces/home/Synaplan/Documents", "/dav/spaces/home/Synaplan/Documents/notes.md"},
		},
		{
			name:     "name taken",
			filename: "notes.md",
			taken: map[string]bool{
				"/dav/spaces/home/Synaplan":                    true,
				"/dav/spaces/home/Synaplan/Documents":          true,
				"/dav/spaces/home/Synaplan/Documents/notes.md": true,
			},
			want:    "Synaplan/Documents/notes (2).md",
			created: []string{"/dav/spaces/home/Synaplan/Documents/notes (2).md"},
		},
		{
			name:     "unknown kind",
			filename: "data.bin",
			taken:    map[string]bool{"/dav/spaces/home/Synaplan": true},
			want:     "Synaplan/data.bin",
			created:  []string{"/dav/spaces/home/Synaplan/data.bin"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var created []string
			srv := fakeCloud(t, tc.taken, &created)
			saved, err := client(srv.URL).Save(context.Background(), tc.filename, []byte("x"))
			if err != nil || saved != tc.want {
				t.Fatalf("saved = %q, err = %v", saved, err)
			}
			if strings.Join(created, "|") != strings.Join(tc.created, "|") {
				t.Fatalf("created = %q, want %q", created, tc.created)
			}
		})
	}
}

func TestATokenFailureStopsBeforeAnyRequest(t *testing.T) {
	c := New("http://127.0.0.1:1", http.DefaultClient, func(context.Context) (string, error) {
		return "", errors.New("no token")
	})
	if _, err := c.Stat(context.Background(), "s!a"); err == nil || !strings.Contains(err.Error(), "no token") {
		t.Fatalf("err = %v", err)
	}
}
