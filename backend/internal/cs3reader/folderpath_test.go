package cs3reader

import (
	"testing"

	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
)

func TestSpaceRelative(t *testing.T) {
	cases := []struct{ in, want string }{
		{"./Projects/Invoices/", "Projects/Invoices"},
		{"/Projects/Invoices", "Projects/Invoices"},
		{"Projects/Invoices", "Projects/Invoices"},
		{".", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := spaceRelative(tc.in); got != tc.want {
				t.Fatalf("spaceRelative(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSameSpace(t *testing.T) {
	root := &provider.ResourceId{StorageId: "s", SpaceId: "ada", OpaqueId: "ada"}
	cases := []struct {
		name string
		id   *provider.ResourceId
		want bool
	}{
		{"a folder in the personal space", &provider.ResourceId{StorageId: "s", SpaceId: "ada", OpaqueId: "f"}, true},
		{"a folder in a project space", &provider.ResourceId{StorageId: "s", SpaceId: "project", OpaqueId: "f"}, false},
		{"a folder on another storage", &provider.ResourceId{StorageId: "t", SpaceId: "ada", OpaqueId: "f"}, false},
		{"no id", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameSpace(tc.id, root); got != tc.want {
				t.Fatalf("sameSpace = %v, want %v", got, tc.want)
			}
		})
	}
}
