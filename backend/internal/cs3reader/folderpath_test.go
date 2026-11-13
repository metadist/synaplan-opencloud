package cs3reader

import "testing"

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
