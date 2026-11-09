package handler

import "testing"

func TestCanonicalPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"./Projects/Invoices/", "Projects/Invoices"},
		{"/Projects/Invoices", "Projects/Invoices"},
		{"Projects/Invoices/", "Projects/Invoices"},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := canonicalPath(tc.in); got != tc.want {
				t.Fatalf("canonicalPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestLooksLikeCode(t *testing.T) {
	if !looksLikeCode("platform_ambiguous") || !looksLikeCode("saved_not_reported") {
		t.Fatal("a machine code must be recognised")
	}
	for _, sentence := range []string{"", "Choose a folder.", "Platform_Ambiguous", "has space"} {
		if looksLikeCode(sentence) {
			t.Fatalf("%q is a sentence, not a code", sentence)
		}
	}
}
