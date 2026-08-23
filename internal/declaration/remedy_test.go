package declaration

import (
	"strings"
	"testing"
)

// Invalid.Remedy is the "how to get unstuck" half of every report about a
// declaration that would not load, and it varies on two axes at once: who owns
// the file, and whether the file could be PARSED at all.
//
// Both axes are load-bearing, and the OLD format lost one of them once —
// introducing the plugin case collapsed the parse axis, so every project-owned
// fault started saying only "disable it", which silently dropped "remove that
// folder if it is not a declaration" from the one case that most needs it. An
// unparseable declaration might not be a declaration at all (a stray file, a note
// left in the folder), so removing the folder is a real way out the merely-invalid
// case does not have; losing it turns a total refusal into a trap. That regression
// was caught only by an end-to-end test, three packages into a long suite. This
// table holds all four combinations directly, so the next person to touch the
// wording finds out in a second.
//
// This is the new-format successor to the old services/sr-session remedy_test.go
// matrix (deleted with the old GUARDRAIL.md loader), re-keyed onto the nature
// format: the local remedy now points at `disabled: [<qualified>]` too (the new
// format has no per-rule `enabled: false`), and the plugin remedy quotes the same
// `disabled: [...]` key and config file the sibling Shadow.Message does — the
// consistency the D1 restore exists to keep.
func TestInvalidRemedy_VariesByOwnerAndByWhetherItParsed(t *testing.T) {
	plugin := Origin{Plugin: "acme", Root: "/cache/acme/0.0.1"}
	malformed := []Problem{{Kind: ErrMalformed, Fault: FaultDeclaration}}
	badMatch := []Problem{{Kind: ErrBadMatch, Fault: FaultDeclaration}}

	cases := []struct {
		name string
		iv   Invalid
		want []string
		deny []string
	}{
		{
			// The project's own file, readable but wrong. It is theirs to fix, and
			// the disable line is one edit away — but it is NOT a plugin, so it must
			// not be told "not yours to fix" or sent to remove the folder.
			name: "project rule that failed validation",
			iv:   Invalid{Nature: NatureGate, Name: "mine", Problems: badMatch},
			want: []string{"fix the gate \"mine\"", ".sloprail", "disabled: [gate/mine]"},
			deny: []string{"not yours to fix", "ships inside plugin", "remove that folder"},
		},
		{
			// The project's own file, unparseable. It may not be a declaration at
			// all, so the way out the merely-invalid case lacks is removing the
			// folder — and the disable line, which edits a file that does not parse,
			// is NOT offered here.
			name: "project rule that could not be parsed",
			iv:   Invalid{Nature: NatureFileGuard, Name: "unreadable", Problems: malformed},
			want: []string{"fix the file-guard \"unreadable\"", "remove that folder"},
			deny: []string{"not yours to fix", "disabled: ["},
		},
		{
			// A plugin's rule. Neither remedy above is available: the consumer
			// cannot edit a file they do not own, and must not remove a folder inside
			// an install cache the next reinstall would restore. The disable list in
			// their own config is the one remedy that is theirs — the same key and
			// file the shadow report names.
			name: "plugin rule that failed validation",
			iv:   Invalid{Nature: NatureGate, Name: "authoring-slop", Origin: plugin, Problems: badMatch},
			want: []string{"not yours to fix", "acme", "disabled: [acme/gate/authoring-slop]", configFile},
			deny: []string{"remove that folder"},
		},
		{
			// Unparseable AND shipped — the worst case, since the consumer owns none
			// of the file. Same remedy, which is the point: the disable list is the
			// only one that is theirs, quoted for the unparseable case too.
			name: "plugin rule that could not be parsed",
			iv:   Invalid{Nature: NatureFileGuard, Name: "broken", Origin: plugin, Problems: malformed},
			want: []string{"not yours to fix", "disabled: [acme/file-guard/broken]"},
			deny: []string{"remove that folder", "fix the"},
		},
		{
			// The structure singleton has no name, so its remedy names the bare
			// nature rather than a quoted name — a plugin's broken structure gate is
			// still disabled by its qualified key, which for the singleton is the
			// bare `<plugin>/structure`.
			name: "plugin structure singleton",
			iv:   Invalid{Nature: NatureStructure, Name: "", Origin: plugin, Problems: badMatch},
			want: []string{"not yours to fix", "disabled: [acme/structure]"},
			deny: []string{"remove that folder"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.iv.Remedy()
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("remedy did not offer %q — a report whose way out is missing is a trap:\n%s", want, got)
				}
			}
			for _, deny := range tc.deny {
				if strings.Contains(got, deny) {
					t.Errorf("remedy offered %q, which does not apply to this case:\n%s", deny, got)
				}
			}
		})
	}
}
