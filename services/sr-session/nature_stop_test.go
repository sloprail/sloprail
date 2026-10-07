package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// joinRefusals is the seam that turns the Stop dispatch's COLLECTED refusals into
// the one block that fails the turn — the rendering half of "at Stop, every rule's
// objection is reported at once, not one turn per rule".
//
// The property is re-tested end to end at tests/e2e/session/024 (two file guards
// both refusing at Stop; the agent sees both), but nothing pinned the collection
// at the unit level: 024 drives the whole dispatch through the mock, so it cannot
// isolate that the JOIN keeps every refusal rather than dropping to the first. The
// dispatch loop (dispatchNatureStop step 1) appends every refused file-guard
// result and hands the whole slice here; these tests pin that this function then
// names all of them, so a regression that quietly rendered only refusals[0] would
// be caught here in a millisecond instead of three packages into the e2e suite.

// No refusals is the empty block — the turn is allowed to end. Distinct from a
// one-entry block, so "nothing refused" never renders as a stray heading.
func TestJoinRefusals_NoneEndsTheTurn(t *testing.T) {
	assert.Equal(t, "", joinRefusals(nil), "no refusal must let the turn end")
	assert.Equal(t, "", joinRefusals([]string{}), "an empty (non-nil) slice must also let the turn end")
}

// A single refusal is rendered bare — no collection heading — because a heading
// over one item is noise, and the agent's whole correction is that one reason.
func TestJoinRefusals_OneIsRenderedBare(t *testing.T) {
	got := joinRefusals([]string{"the goal is not done (gate \"goal-complete\")"})

	assert.Equal(t, "the goal is not done (gate \"goal-complete\")", got)
	assert.NotContains(t, got, "the following rules", "a single refusal must not wear the multi-refusal heading")
}

// Several refusals are ALL rendered — the property the audit names. The agent is
// about to spend a turn on this, and reporting one at a time turns one correction
// into as many turns as there are rules; so every refusal must survive the join,
// each attributable to its own rule.
// sr:proves session/turn-end-refusal-is-one-block-naming-each-rule
func TestJoinRefusals_AllCollectedNotStoppedAtFirst(t *testing.T) {
	refusals := []string{
		"secrets must not be committed (file-guard \"no-secrets\")",
		"the changelog was not updated (file-guard \"changelog\")",
		"the goal is not done (gate \"goal-complete\")",
	}
	got := joinRefusals(refusals)

	// Every refusal is present — none dropped at the first.
	for _, r := range refusals {
		assert.Contains(t, got, r, "a collected refusal was lost — the join must keep every rule's objection, not stop at the first")
	}
	// And they are presented as a list the agent can read rule by rule.
	assert.Contains(t, got, "the following rules refused this turn's work:")
	assert.Equal(t, 3, strings.Count(got, "\n  - "), "each collected refusal must be its own bulleted line")
}

// The Stop dispatch's order is load-bearing: context enters run FIRST, so
// commit-required, the file-guards (match, `when`, checks) and the gates all read
// context[] as this turn left it, and context exits run LAST, so what a rule
// reads at this Stop is still open. The steps are ordinary calls in one function,
// so the order is pinned by where each call sits in dispatchNatureStop's body.
func TestDispatchNatureStop_StepOrder(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "nature_stop.go", nil, 0)
	require.NoError(t, err)

	steps := []string{"runContextEnters", "commitRequired", "runGatesForEvents", "runContextExits"}
	pos := map[string]token.Pos{}
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "dispatchNatureStop" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok {
				for _, s := range steps {
					if id.Name == s {
						if _, seen := pos[s]; !seen {
							pos[s] = call.Pos()
						}
					}
				}
			}
			return true
		})
	}
	for _, s := range steps {
		require.Contains(t, pos, s, "dispatchNatureStop no longer calls %s", s)
	}
	for i := 1; i < len(steps); i++ {
		assert.Less(t, pos[steps[i-1]], pos[steps[i]],
			"%s must run before %s (enters -> commit-required -> gates -> exits)", steps[i-1], steps[i])
	}
}
