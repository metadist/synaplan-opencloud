package handler

import (
	"bytes"
	"mime/multipart"
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
)

func TestTheOriginalNameIsThePathInTheSpace(t *testing.T) {
	cases := []struct {
		path, want string
	}{
		{"./Documents/a.txt", "/Documents/a.txt"},
		{"Documents/a.txt", "/Documents/a.txt"},
		{"/Documents/a.txt", "/Documents/a.txt"},
		{"", "a.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			var buf bytes.Buffer
			mw := multipart.NewWriter(&buf)
			if err := writeProvenance(mw, &cs3reader.File{Name: "a.txt", Path: tc.path}, nil); err != nil {
				t.Fatal(err)
			}
			_ = mw.Close()
			form, err := multipart.NewReader(&buf, mw.Boundary()).ReadForm(1 << 10)
			if err != nil {
				t.Fatal(err)
			}
			if got := form.Value["original_name"]; len(got) != 1 || got[0] != tc.want {
				t.Fatalf("original_name = %v, want %q", got, tc.want)
			}
		})
	}
}
