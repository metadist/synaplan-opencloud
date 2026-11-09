package davfiles

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
)

const folderListing = `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns">
<d:response><d:href>/dav/spaces/s%21f/</d:href><d:propstat><d:prop>
<oc:fileid>s!f</oc:fileid><oc:name>Projects</oc:name><d:getetag>"e0"</d:getetag><d:resourcetype><d:collection/></d:resourcetype>
</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>
<d:response><d:href>/dav/spaces/s%21f/a.txt</d:href><d:propstat><d:prop>
<oc:fileid>s!a</oc:fileid><oc:name>a.txt</oc:name><d:getetag>"e1"</d:getetag><d:getcontenttype>text/plain</d:getcontenttype><d:getcontentlength>5</d:getcontentlength><d:resourcetype/>
</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>
</d:multistatus>`

const metaPath = `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:response><d:propstat><d:prop>
<oc:meta-path-for-user>/Projects</oc:meta-path-for-user></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`

func fakeCloud(t *testing.T, taken map[string]bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer person" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == "PROPFIND" && strings.HasPrefix(r.URL.Path, "/dav/meta/"):
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = io.WriteString(w, metaPath)
		case r.Method == "PROPFIND" && r.URL.Path == "/dav/spaces/s!f":
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = io.WriteString(w, folderListing)
		case r.Method == "PROPFIND":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut:
			if taken[r.URL.Path] {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			w.Header().Set("Oc-Fileid", "s!new")
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
}

func client(url string) *Client {
	return New(url, http.DefaultClient, func(context.Context) (string, error) { return "person", nil })
}

func TestListReturnsChildrenWithTheirPersonRelativePath(t *testing.T) {
	srv := fakeCloud(t, nil)
	defer srv.Close()
	children, err := client(srv.URL).List(context.Background(), "s!f")
	if err != nil {
		t.Fatal(err)
	}
	want := cs3reader.Child{ID: "s!a", Name: "a.txt", Path: "Projects/a.txt", Etag: `"e1"`, MimeType: "text/plain", Size: 5}
	if len(children) != 1 || children[0] != want {
		t.Fatalf("children = %+v", children)
	}
}

func TestStatAnyReportsAMissingResourceAsNotFound(t *testing.T) {
	srv := fakeCloud(t, nil)
	defer srv.Close()
	if _, err := client(srv.URL).StatAny(context.Background(), "s!gone"); !errors.Is(err, cs3reader.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSaveChildPicksAFreeName(t *testing.T) {
	cases := []struct {
		name  string
		taken map[string]bool
		want  string
	}{
		{"free", nil, "Projects/notes.md"},
		{"taken once", map[string]bool{"/dav/spaces/s!f/notes.md": true}, "Projects/notes (2).md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := fakeCloud(t, tc.taken)
			defer srv.Close()
			written, err := client(srv.URL).SaveChild(context.Background(), "s!f", "notes.md", []byte("x"))
			if err != nil || written.Path != tc.want || written.ID != "s!new" {
				t.Fatalf("written = %+v, err = %v", written, err)
			}
		})
	}
}

func TestATokenFailureStopsBeforeAnyRequest(t *testing.T) {
	c := New("http://127.0.0.1:1", http.DefaultClient, func(context.Context) (string, error) { return "", errors.New("not connected") })
	if _, err := c.List(context.Background(), "s!f"); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("err = %v", err)
	}
}
