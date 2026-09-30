package e2e

import (
	"testing"
)

// disabled_guardrail_inert: a guardrail the project has switched off contributes
// no check runs and no refusals.
//
// "Contributes no check runs" is the part a refusal check cannot see. A rule that
// runs its check and has the verdict discarded looks exactly like one that never
// ran, from the stream — and the two are different: the first still pays for the
// check, and still lets it write files, read the tree, or make a network call. So
// these tests read the check's own ledger, not just the outcome.
//
// The old format switched a rule off with `enabled: false` INSIDE the declaration.
// The new format keeps that decision on the CONSUMER's side, in
// `.sloprail/config.yaml`'s `disabled:` list, keyed on the qualified name — for a
// project's own gate, `gate/<name>` (internal/declaration/config.go,
// applied by store.go's applyDisable, which filters the disabled declaration out
// of the loaded set entirely so it never dispatches and no check runs). This is
// the right home: a plugin's `enabled: false` edit lives in an install cache the
// next reinstall overwrites, so the switch-off has to survive on the project's
// side. These tests install a NEW-format gate and disable it that way, then
// re-prove the SAME inertness against the NEW pre-tool dispatch.
//
// Every "did not happen" test below is paired with the same declaration left
// enabled. Without the pair, a test asserting nothing happened would still pass if
// the guardrail were misspelled, unparseable, or bound to a match nothing hits —
// proving only that the test cannot fail.

// switchableGuard is a NEW-FORMAT gate on PreFileWrite that refuses every
// markdown write, recording each run first. A gate, so a refusal is observable as a
// pre-tool deny; the ledger under the gate's own folder (SR_GUARDRAIL_DIR) is how a RUN is
// observed independently of the verdict. It is the SAME declaration whether or not
// the project disables it — only config.yaml differs, so the enabled case is a
// genuine control.
const switchableGuard = `on:
  - event: PreFileWrite
    match: event.path endsWith ".md"
checks:
  - script: ./refuse.sh
`

// The check records that it ran BEFORE refusing, so a run is visible whether or
// not the refusal is acted on. A check that only refused would leave a disabled
// guardrail that still runs its checks indistinguishable from one that does not.
const recordThenRefuse = `#!/bin/sh
cat >/dev/null
echo ran >> "$SR_GUARDRAIL_DIR/ledger"
echo '{"reason":"this rule is in force"}'
exit 1
`

// recordThenPermit records that it ran and then allows the work.
const recordThenPermit = `#!/bin/sh
cat >/dev/null
echo ran >> "$SR_GUARDRAIL_DIR/ledger"
exit 0
`

// T011_01: enabled, the rule runs its check and refuses.
//
// The control. It establishes that this declaration, this match and this write do
// reach the check — which is what makes the disabled case below meaningful rather
// than vacuous.
func TestT011_01_EnabledGuardrailRunsAndRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "switchable", switchableGuard, map[string]string{"refuse.sh": recordThenRefuse})

	got := e.Run(proj, "s-011-01", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	if !got.Refused() {
		t.Fatalf("the enabled guardrail did not refuse — the disabled case below would prove nothing:\n%s", got.Output)
	}
	if !got.Saw("this rule is in force") {
		t.Fatalf("the enabled guardrail's reason did not reach the agent:\n%s", got.Output)
	}
	if runs := len(e.GateLedgerLines(proj, "switchable", "ledger")); runs != 1 {
		t.Fatalf("the enabled guardrail's check ran %d times, want 1", runs)
	}
}

// T011_02: switched off, the same rule neither runs its check nor refuses.
//
// Both halves asserted. "No refusal" alone would be satisfied by an engine that
// runs every disabled guardrail's checks and ignores what they say — inert in the
// verdict, but not inert in what it costs or in what those checks do on the way.
func TestT011_02_DisabledGuardrailNeitherRunsNorRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "switchable", switchableGuard, map[string]string{"refuse.sh": recordThenRefuse})
	// Switched off from the project's own side, by qualified name.
	e.DisablePluginGuardrail(proj, "gate/switchable")

	got := e.Run(proj, "s-011-02", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	if got.Refused() {
		t.Errorf("a disabled guardrail refused the work:\n%s", got.Output)
	}
	if got.Saw("this rule is in force") {
		t.Errorf("a disabled guardrail's reason reached the agent:\n%s", got.Output)
	}
	if runs := len(e.GateLedgerLines(proj, "switchable", "ledger")); runs != 0 {
		t.Errorf("a disabled guardrail ran its check %d times, want none", runs)
	}
	// The write the disabled guard would have blocked actually landed.
	if !e.Exists(proj, "some/notes.md") {
		t.Errorf("the write did not land even though the only rule was disabled")
	}
}

// T011_03: disabling one rule leaves the others enforcing, and the disabled one is
// still passed over on a run that reaches it.
//
// Turning a rule off is one rule's decision. An engine that let a disabled
// declaration suppress the load, or take its neighbours down with it, would turn a
// deliberate switch-off into a silent disarming of the whole project — and the
// project would look guarded.
//
// Both gates bind the SAME match here, so the disabled one is genuinely a
// candidate the dispatch would otherwise reach on this write. The enabled rule
// PERMITS (recording that it ran) rather than refuses, so its being in force is
// proven by its ledger rather than by a deny that could have come from either — a
// disabled rule that still refused would be visible even though the enabled one is
// the only one that should have run.
func TestT011_03_DisablingOneLeavesTheOthersInForce(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "switchable", switchableGuard, map[string]string{"refuse.sh": recordThenRefuse})
	e.Gate(proj, "still-on", switchableGuard, map[string]string{"refuse.sh": recordThenPermit})
	e.DisablePluginGuardrail(proj, "gate/switchable")

	got := e.Run(proj, "s-011-03", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	// The enabled rule ran. Read from its ledger, not the stream: it permits, and a
	// permitted check's stderr reaches no agent. Without this the disabled-side
	// assertion below could hold because nothing ran at all. What
	// matters here is that it ran, against the disabled rule's zero.
	if runs := len(e.GateLedgerLines(proj, "still-on", "ledger")); runs == 0 {
		t.Fatalf("the enabled guardrail's check never ran — the disabled-side assertion below would prove nothing:\n%s", got.Output)
	}
	// And the disabled rule, bound to the same match and reached on the same pass,
	// did not.
	if runs := len(e.GateLedgerLines(proj, "switchable", "ledger")); runs != 0 {
		t.Errorf("the disabled guardrail ran its check %d times, want none", runs)
	}
	// Nor did it refuse. The rule's own words never reached the agent.
	if got.Saw("this rule is in force") {
		t.Errorf("a disabled guardrail refused the work:\n%s", got.Output)
	}
}
