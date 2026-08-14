package main

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/guardrail"
)

// remedy is the "how to get unstuck" half of every refusal caused by a rule that
// would not load, and it varies on two axes at once: who owns the file, and
// whether the file could be PARSED at all.
//
// Both axes are load-bearing and one of them was got wrong. Introducing the
// plugin case collapsed the parse axis — every project-owned fault started
// saying "fix the declaration, or disable it with `enabled: false`" — which
// silently dropped "remove that folder if it is not a guardrail" from the one
// case that most needs it. An unparseable declaration refuses EVERY action
// rather than a scoped set, and it may not be a guardrail at all (a stray file,
// a note left in the folder), so removing the folder is a real way out that the
// merely-invalid case does not have. Losing it turns a total refusal into a trap.
//
// That regression was caught only by an end-to-end test (T013_07), three
// packages into a forty-minute suite. This table holds all four combinations
// directly, so the next person to touch the wording finds out in a second.
func TestRemedy_VariesByOwnerAndByWhetherItParsed(t *testing.T) {
	plugin := guardrail.Origin{Plugin: "sloprail", Root: "/cache/sloprail/0.0.1"}

	cases := []struct {
		name string
		iv   guardrail.Invalid
		want []string
		deny []string
	}{
		{
			// The project's own file, readable but wrong. It is theirs to fix,
			// and `enabled: false` is one line away.
			name: "project rule that failed validation",
			iv:   guardrail.Invalid{Name: "mine"},
			want: []string{"fix the declaration in mine", "enabled: false"},
			deny: []string{"not yours to fix", "disabled: ["},
		},
		{
			// The project's own file, unparseable. `enabled: false` cannot be
			// added to a file that does not parse as a declaration, so the way
			// out is removing the folder.
			name: "project rule that could not be parsed",
			iv: guardrail.Invalid{
				Name:     "unreadable",
				Problems: []guardrail.Problem{{Kind: guardrail.ErrMalformed, Binding: -1, Hook: -1}},
			},
			want: []string{"fix the declaration in unreadable", "remove that folder"},
			deny: []string{"enabled: false", "not yours to fix"},
		},
		{
			// A plugin's rule. Neither remedy above is available: the consumer
			// cannot edit a file they do not own, and must not remove a folder
			// inside an install cache that the next reinstall would restore.
			name: "plugin rule that failed validation",
			iv:   guardrail.Invalid{Name: "authoring-slop", Origin: plugin},
			want: []string{"not yours to fix", "sloprail", "disabled: [sloprail/authoring-slop]", ".sloprail/config.yaml"},
			deny: []string{"enabled: false", "remove that folder"},
		},
		{
			// Unparseable AND shipped — the worst case, since it refuses every
			// action and the consumer owns none of the file. Same remedy, which
			// is the point: the disable list is the only one that is theirs.
			name: "plugin rule that could not be parsed",
			iv: guardrail.Invalid{
				Name:     "broken",
				Origin:   plugin,
				Problems: []guardrail.Problem{{Kind: guardrail.ErrMalformed, Binding: -1, Hook: -1}},
			},
			want: []string{"not yours to fix", "disabled: [sloprail/broken]"},
			deny: []string{"enabled: false", "remove that folder"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := remedy(tc.iv)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("remedy did not offer %q — a refusal whose way out is missing is a trap:\n%s", want, got)
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
