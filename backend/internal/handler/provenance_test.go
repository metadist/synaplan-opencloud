package handler

import (
	"bytes"
	"mime/multipart"
	"slices"
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

func TestKeepSyncedIsSentOnlyWhenSet(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name string
		keep *bool
		want []string
	}{
		{"unset leaves what Synaplan has", nil, nil},
		{"asked", &yes, []string{"1"}},
		{"not asked turns it off", &no, []string{"0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			mw := multipart.NewWriter(&buf)
			if err := writeProvenance(mw, &cs3reader.File{Name: "a.txt"}, &uploadProvenance{SourceID: "r", KeepSynced: tc.keep}); err != nil {
				t.Fatal(err)
			}
			_ = mw.Close()
			form, err := multipart.NewReader(&buf, mw.Boundary()).ReadForm(1 << 10)
			if err != nil {
				t.Fatal(err)
			}
			if got := form.Value["keep_synced"]; !slices.Equal(got, tc.want) {
				t.Fatalf("keep_synced = %v, want %v", got, tc.want)
			}
		})
	}
}
