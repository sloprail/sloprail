package e2e

import (
	"fmt"
	"testing"
)

// disabled_guardrail_inert: a guardrail whose `enabled` is false contributes no
// hook runs and no refusals.
//
// "Contributes no hook runs" is the part a refusal check cannot see. A rule that
// runs its hook and has the verdict discarded looks exactly like one that never
// ran, from the stream — and the two are different: the first still pays for the
// hook, and still lets it write files, read the tree, or make a network call.
// So these tests read the hook's own ledger, not just the outcome.
//
// Every "did not happen" test below is paired with the same declaration switched
// on. Without the pair, a test asserting nothing happened would still pass if
// the guardrail were misspelled, unparseable, or bound to a kind that never
// fires — proving only that the test cannot fail.
//
// The engine tests `enabled` twice, and the two checks can mask each other, so
// the tests below are split to hold each one alone:
//
//   - T011_02 has the disabled rule ALONE. Its kind never enters the bound set,
//     so no module runs and no event exists. This is the collection-time check;
//     it is all that stands between the project and a hook run here.
//   - T011_03 puts an ENABLED rule on the same kind, and that rule PERMITS. The
//     kind is now bound whatever `enabled` says about the disabled rule, an event
//     exists, and dispatch walks the declarations — so the disabled rule's hook is
//     the next thing that would run. Only the dispatch-time check stops it.
//
// The permit matters. Dispatch returns at the first refusal, so an enabled rule
// that refused would end the pass before the disabled rule was ever considered,
// and T011_03 would keep passing with the dispatch check deleted.

// declaration is the same rule either way. Only the `enabled` line differs, so
// the enabled case is a genuine control: anything that would silence the
// disabled case for some other reason silences the enabled case too, and the
// pair fails together rather than passing together.
func declaration(enabled bool) string {
	return fmt.Sprintf(`---
enabled: %t
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses every write, when it is switched on

Turning a rule off is a declaration rather than a deletion, so the reasoning
that produced it survives the decision to stop enforcing it.
`, enabled)
}

// The hook records that it ran BEFORE refusing, so a run is visible whether or
// not the refusal is acted on. A hook that only refused would leave a disabled
// guardrail that still runs its hooks indistinguishable from one that does not.
const recordThenRefuse = `#!/bin/sh
cat >/dev/null
echo ran >> "$PWD/ledger"
echo "this rule is in force" >&2
exit 1
`

// T011_01: switched on, the rule runs its hook and refuses.
//
// The control. It establishes that this declaration, this binding and this
// write do reach the hook — which is what makes the disabled case below
// meaningful rather than vacuous.
func TestT011_01_EnabledGuardrailRunsAndRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "switchable", declaration(true), map[string]string{"refuse.sh": recordThenRefuse})

	got := e.Run(proj, "s-011-01", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	if !got.Saw("this rule is in force") {
		t.Fatalf("the enabled guardrail did not refuse — the disabled case below would prove nothing:\n%s", got.Output)
	}
	if runs := e.Ledger(proj, "switchable", "ledger"); len(runs) != 1 {
		t.Fatalf("the enabled guardrail's hook ran %d times, want 1: %v", len(runs), runs)
	}
}

// T011_02: switched off, the same rule neither runs its hook nor refuses.
//
// Both halves asserted. "No refusal" alone would be satisfied by an engine that
// runs every disabled guardrail's hooks and ignores what they say — inert in the
// verdict, but not inert in what it costs or in what those hooks do on the way.
func TestT011_02_DisabledGuardrailNeitherRunsNorRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "switchable", declaration(false), map[string]string{"refuse.sh": recordThenRefuse})

	got := e.Run(proj, "s-011-02", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	if got.Saw("this rule is in force") {
		t.Errorf("a disabled guardrail refused the work:\n%s", got.Output)
	}
	if runs := e.Ledger(proj, "switchable", "ledger"); len(runs) != 0 {
		t.Errorf("a disabled guardrail ran its hook %d times, want none: %v", len(runs), runs)
	}
}

// recordThenPermit records that it ran and then allows the work. The enabled
// half of T011_03 needs this rather than a refusal: dispatch stops at the first
// refusal, so an enabled rule that refuses ends the pass before any later
// declaration is considered, and a disabled rule sitting behind it is never
// reached for reasons that have nothing to do with `enabled`.
const recordThenPermit = `#!/bin/sh
cat >/dev/null
echo ran >> "$PWD/ledger"
echo "the other rule still holds" >&2
exit 0
`

// T011_03: disabling one rule leaves the others enforcing, and the disabled one
// is still passed over on a run that reaches it.
//
// Turning a rule off is one rule's decision. An engine that let a disabled
// declaration suppress the loading pass, or abandon the rest of the list, would
// turn a deliberate switch-off into a silent disarming of the whole project —
// and the project would look guarded.
//
// The enabled rule here PERMITS. That is what makes this the probe for the
// dispatch-time `enabled` check rather than a second copy of T011_02. Both rules
// bind the same kind, so the disabled one's hook is the next thing dispatch would
// reach; it is skipped only because dispatch tests `enabled` again. Were the
// enabled rule to refuse instead, the pass would return at that refusal and the
// disabled hook would go unrun no matter what the engine believes about
// `enabled` — the test would pass with the check deleted.
//
// This is the pairing that closes the mask between the two `enabled` filters:
// T011_02 (the disabled rule is alone, so collection keeps its kind out of the
// bound set) and this one (collection admits the kind for the enabled rule, so
// only the dispatch filter can keep the disabled rule's hook from running).
func TestT011_03_DisablingOneLeavesTheOthersInForce(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "switchable", declaration(false), map[string]string{"refuse.sh": recordThenRefuse})
	e.Guardrail(proj, "still-on", declaration(true), map[string]string{"refuse.sh": recordThenPermit})

	got := e.Run(proj, "s-011-03", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	// The enabled rule ran. Read from its ledger, not the stream: this hook
	// permits, and a permitted hook's stderr is not carried back to the agent.
	// Without this the disabled-side assertion below could hold because nothing
	// ran at all.
	if runs := e.Ledger(proj, "still-on", "ledger"); len(runs) != 1 {
		t.Fatalf("the enabled guardrail's hook ran %d times, want 1 — the disabled-side assertion below would prove nothing: %v\n%s", len(runs), runs, got.Output)
	}
	// And the disabled rule, bound to the same kind and reached on the same
	// pass, did not.
	if runs := e.Ledger(proj, "switchable", "ledger"); len(runs) != 0 {
		t.Errorf("the disabled guardrail ran its hook %d times, want none: %v", len(runs), runs)
	}
	// Nor did it refuse. The rule's own words never reached the agent.
	if got.Saw("this rule is in force") {
		t.Errorf("a disabled guardrail refused the work:\n%s", got.Output)
	}
}
