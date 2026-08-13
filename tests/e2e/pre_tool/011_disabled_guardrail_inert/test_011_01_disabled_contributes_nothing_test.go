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

// T011_03: disabling one rule leaves the others enforcing.
//
// Turning a rule off is one rule's decision. An engine that let a disabled
// declaration suppress the loading pass, or abandon the rest of the list, would
// turn a deliberate switch-off into a silent disarming of the whole project —
// and the project would look guarded.
func TestT011_03_DisablingOneLeavesTheOthersInForce(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "switchable", declaration(false), map[string]string{"refuse.sh": recordThenRefuse})
	e.Guardrail(proj, "still-on", declaration(true), map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> \"$PWD/ledger\"\necho 'the other rule still holds' >&2\nexit 1\n",
	})

	got := e.Run(proj, "s-011-03", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	if !got.Saw("the other rule still holds") {
		t.Fatalf("a disabled guardrail suppressed an enabled one:\n%s", got.Output)
	}
	if runs := e.Ledger(proj, "switchable", "ledger"); len(runs) != 0 {
		t.Errorf("the disabled guardrail ran its hook %d times, want none: %v", len(runs), runs)
	}
}
