package handler

import "testing"

func TestGeneratedFilePath(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/api/v1/files/uploads/7/cat.png", "/api/v1/files/uploads/7/cat.png", true},
		{"https://synaplan.example/api/v1/files/uploads/7/cat.png?x=1", "/api/v1/files/uploads/7/cat.png", true},
		{"/api/v1/files/42/download", "/api/v1/files/42/download", true},
		{"/api/v1/files/42/content", "", false},
		{"/api/v1/files/uploads/7/../../admin", "", false},
		{"/api/v1/files/uploads/seven/cat.png", "", false},
		{"/api/v1/admin/users", "", false},
		{"http://evil.example/", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := generatedFilePath(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("generatedFilePath(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
