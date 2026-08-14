package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sloprail/sloprail/internal/guardrail"
)

// extractor_runs_bound, the half no end-to-end test can see.
//
// The invariant has two parts: a module runs only when a binding names a kind it
// produces, AND that binding belongs to a guardrail that is enabled. The second
// part is invisible downstream — a disabled guardrail's hook is stopped again at
// dispatch, so collecting its kinds changes no observable outcome. The only
// difference is that a module ran to produce events nobody could act on, and
// cost is precisely what the spec's `why` cites as the reason this exists.
//
// Which is why it is tested here rather than through the harness. Removing the
// enabled check left the entire repository's suite green, measured.
func TestBoundKinds_ADisabledGuardrailAsksForNothing(t *testing.T) {
	off := false
	disabled := guardrail.Declaration{
		Name:    "off",
		Enabled: &off,
		Hooks:   map[string][]guardrail.Binding{"PreCommandInvoke": {{}}},
	}

	assert.Empty(t, boundKinds([]guardrail.Declaration{disabled}, nil),
		"a disabled guardrail's kinds were collected, so its module runs to produce events nothing will act on")
}

// The control, without which the assertion above is satisfied by a function that
// collects nothing at all.
func TestBoundKinds_AnEnabledGuardrailAsksForItsKinds(t *testing.T) {
	enabled := guardrail.Declaration{
		Name:  "on",
		Hooks: map[string][]guardrail.Binding{"PreCommandInvoke": {{}}},
	}

	assert.Equal(t, []string{"PreCommandInvoke"}, boundKinds([]guardrail.Declaration{enabled}, nil))
}

// A declaration that did not parse has no `enabled` to read, so its kinds are
// collected whatever state it is in — and they must be, because an event of that
// kind occurring is the only thing that can report the rule as unenforced.
func TestBoundKinds_ABrokenDeclarationIsNotFilteredOnEnabled(t *testing.T) {
	broken := guardrail.Invalid{
		Name: "broken",
		Problems: []guardrail.Problem{
			{Kind: guardrail.ErrBadMatcher, Event: "PreFileCreate", Binding: 0, Hook: -1},
		},
	}

	assert.Equal(t, []string{"PreFileCreate"}, boundKinds(nil, []guardrail.Invalid{broken}),
		"a broken rule's kinds are what has stopped being enforced; leaving them out makes the one case that must be reported the one case no event is produced for")
}
