package handler

import (
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/state"
)

func TestScopeAllows(t *testing.T) {
	watched := []indexWatch{{ID: 1, TargetKind: "folder", TargetID: "f$1!invoices", Owner: "ada"}}
	named := state.ReadScope{Mode: "allowed", Folders: []state.Folder{{ID: "f$1!hr", Path: "HR"}}}
	cases := []struct {
		name  string
		scope state.ReadScope
		id    string
		path  string
		want  bool
	}{
		{"watched mode allows a watched folder", state.ReadScope{Mode: "watched"}, "f$1!invoices", "Invoices", true},
		{"watched mode refuses an unwatched folder", state.ReadScope{Mode: "watched"}, "f$1!hr", "HR", false},
		{"an empty mode behaves as watched", state.ReadScope{}, "f$1!other", "Other", false},
		{"allowed mode allows a named folder", named, "f$1!hr", "HR", true},
		{"allowed mode allows a subfolder of a named one", named, "f$1!hr-2026", "HR/2026", true},
		{"allowed mode still allows a watched folder", named, "f$1!invoices", "Invoices", true},
		{"allowed mode refuses the rest", named, "f$1!private", "Private", false},
		{"all mode allows everything", state.ReadScope{Mode: "all"}, "f$1!private", "Private", true},
		{"no id is never allowed", state.ReadScope{Mode: "all"}, "", "Private", false},
		{"an unknown mode is refused", state.ReadScope{Mode: "sure"}, "f$1!invoices", "Invoices", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scopeAllows(tc.scope, watched, tc.id, tc.path); got != tc.want {
				t.Fatalf("scopeAllows() = %v, want %v", got, tc.want)
			}
		})
	}
}
