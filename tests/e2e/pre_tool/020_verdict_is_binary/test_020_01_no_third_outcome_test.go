// Package e2e drives the one enforcement invariant nothing referenced.
//
// verdict_is_binary: "A check either refuses the work or permits it, with no
// third outcome that records an objection while allowing the work to proceed."
// Why: "An advisory tier is where rules go to be ignored. A rule worth
// declaring is worth enforcing, and a warning an agent may disregard is
// indistinguishable from no rule at all."
//
// An audit of enforcement.tsp found this the only one of its eleven invariants
// with no test anywhere in the tree — by name or by property. The behaviour IS
// implemented, and structurally so: the check-runner's Verdict carries a bool
// (internal/dispatch), and a check that decided nothing is a refusal rather than
// a silent pass. There is no advisory branch to find. But "the code has no third
// state" is a claim about the code, and the invariant is a claim about what a
// CHECK AUTHOR can achieve — which is a different question, because a check is an
// arbitrary program free to print whatever it likes.
//
// So these tests come at it from the author's side. Each one is a check
// genuinely TRYING to record an objection while letting the work through, using
// a vocabulary that exists somewhere in the ecosystem, and each asserts the
// attempt collapses into one of the two outcomes there are.
//
// The binary verdict is a PRE-ACTION property — a pending write either lands or
// it does not — so the vehicle is a GATE on the pre-write event. The new-format
// check contract is where "binary" now lives: exit 0 permits, exit non-zero
// refuses (a `{"reason":...}` on stdout is the reason; scriptRefusalReason reads
// the exit STATUS, never a printed decision). So the old "third outcome" attempts
// — the OLD hook protocol's `{"decision":"block"}` at exit 0, its
// `permissionDecision:"ask"` — are re-expressed as the SAME shape a new check
// author would reach for: printing an objection (a reason document, or a plain
// line) while exiting 0. It permits, and the objection reaches nobody, because a
// check at exit 0 permits and its stdout is not read as a verdict. That is the
// failure the `why` describes, and T020_01 shows it happening. Refusals are
// observed with res.Refused() (the pre-tool deny marker) and the tree; the
// gate's own ledger under `.sloprail/gate/<name>/` proves the check ran.

package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bindEveryCreate is a gate that runs one check on every file creation. What
// varies across these tests is only what the check says and how it exits.
const bindEveryCreate = `on:
  - event: PreFileCreate
checks:
  - script: ./h.sh
`

// T020_01: a check that prints a refusal DOCUMENT and exits zero does NOT block.
//
// The purest attempt at the forbidden third outcome, and the one a check author
// is most likely to write by accident: a `{"reason":...}` — the very shape the
// engine reads on a REFUSAL — paired with a success exit. If anything were going
// to record an objection while permitting the work, it would be this.
//
// It permits, and — the half that matters for the `why` — the objection reaches
// nobody. The reason text is not delivered, because a check that exits 0 permits
// and its stdout is not consulted for a verdict. So the rule did not become
// advisory; it became silent, which is what "indistinguishable from no rule at
// all" means concretely.
//
// The LEDGER assertion is what stops this being vacuous. Asserting only that the
// write landed would pass on an engine that had never run the check, and the
// ledger below is what separates those: the rule WAS asked, said its piece, and
// its piece went nowhere.
func TestT020_01_AnObjectionAtExitZeroPermitsAndIsNotDelivered(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "objector", bindEveryCreate, map[string]string{
		"h.sh": `#!/bin/sh
cat >/dev/null
echo asked >> "$SR_GUARDRAIL_DIR/log"
echo '{"reason":"MARK-I-object-but-carry-on"}'
exit 0
`,
	})

	res := e.Run(proj, "s-020-01", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	// The rule really was consulted. Without this the two assertions below are
	// satisfied by a gate that never loaded.
	require.Equal(t, []string{"asked"}, e.GateLedgerLines(proj, "objector", "log"),
		"the check must have run, or nothing here is about what it decided")

	assert.False(t, res.Refused(), "exit zero permits, whatever the check printed")
	assert.True(t, e.Exists(proj, "notes.md"),
		"the work proceeds — there is no outcome that objects and still allows it")
	assert.False(t, res.Saw("MARK-I-object-but-carry-on"),
		"and the objection reaches nobody: a check at exit 0 permits and its stdout is not a verdict channel")
}

// T020_02: a plain-prose objection at exit zero is not a third outcome either.
//
// The other vocabulary an author might reach for: not the structured reason
// document but an ordinary line of prose meant to warn, still at a success exit.
// A check's plain stdout is read only when it REFUSES (as the reason); at exit 0
// it is not read at all. This drives that end to end.
//
// Distinct from T020_01 rather than a duplicate of it: that one is the engine's
// own refusal vocabulary at the wrong exit status, this one is a message with no
// structure at all — the thing a debugging `echo` leaves behind. If any input
// could produce a third outcome it would be one of these two.
func TestT020_02_ProsePrintedAtExitZeroIsNeitherARefusalNorDelivered(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "asker", bindEveryCreate, map[string]string{
		"h.sh": `#!/bin/sh
cat >/dev/null
echo asked >> "$SR_GUARDRAIL_DIR/log"
echo 'MARK-please-confirm-this'
exit 0
`,
	})

	res := e.Run(proj, "s-020-02", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	require.Equal(t, []string{"asked"}, e.GateLedgerLines(proj, "asker", "log"),
		"the check must have run")

	assert.False(t, res.Refused(), `prose at exit 0 does not refuse`)
	assert.True(t, e.Exists(proj, "notes.md"), "the work proceeds")
	assert.False(t, res.Saw("MARK-please-confirm-this"),
		`the message does not reach the agent either — it is silence, not a middle tier`)
}

// T020_03: a check claiming approval while exiting non-zero is still a refusal.
//
// The mirror image, and the one that pins which side wins when a check
// contradicts itself. An engine reading printed text in preference to the exit
// status would let a check approve work by saying so — and since a non-zero exit
// is how every BROKEN check fails, that would hand every crashing script a way to
// claim consent.
//
// The exit status governs, and the printed reason is used only as the wording.
// Both halves are asserted: the work was stopped, and the agent was told
// something rather than nothing.
func TestT020_03_AClaimOfApprovalDoesNotSurviveANonZeroExit(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "contradictor", bindEveryCreate, map[string]string{
		"h.sh": `#!/bin/sh
cat >/dev/null
echo asked >> "$SR_GUARDRAIL_DIR/log"
echo '{"reason":"MARK-approved-anyway"}'
exit 1
`,
	})

	res := e.Run(proj, "s-020-03", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	require.Equal(t, []string{"asked"}, e.GateLedgerLines(proj, "contradictor", "log"),
		"the check must have run")

	assert.True(t, res.Refused(),
		"a non-zero exit refuses, whatever the check printed")
	assert.False(t, e.Exists(proj, "notes.md"),
		"and the work really is prevented, not merely reported as refused")
	assert.True(t, res.Saw("MARK-approved-anyway"),
		"the check's own words become the reason — the status decides, the text explains")
}

// T020_04: the two outcomes are genuinely distinguishable.
//
// The control the three tests above need. Each of them asserts an attempted
// third outcome collapsed to permit or to refuse — claims that would all pass
// on an engine stuck permanently in one state. This runs the same gate with
// a plainly-permitting and a plainly-refusing check and shows the two produce
// different answers on the tree.
//
// Without it, T020_01 and T020_02 are satisfied by an engine that permits
// everything, and T020_03 by one that refuses everything. Two of those three
// would be green on an engine with no enforcement whatsoever.
func TestT020_04_BothOutcomesAreReachable(t *testing.T) {
	e := New(t)

	permit := e.Project()
	e.Gate(permit, "yes", bindEveryCreate, map[string]string{
		"h.sh": "#!/bin/sh\ncat >/dev/null\nexit 0\n",
	})
	permitRes := e.Run(permit, "s-020-04a", "write", Turns("done", Write("w1", "notes.md", "hello")))

	refuse := e.Project()
	e.Gate(refuse, "no", bindEveryCreate, map[string]string{
		"h.sh": "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"MARK-refused\"}'\nexit 1\n",
	})
	refuseRes := e.Run(refuse, "s-020-04b", "write", Turns("done", Write("w1", "notes.md", "hello")))

	assert.False(t, permitRes.Refused(), "a permitting check permits")
	assert.True(t, e.Exists(permit, "notes.md"), "and the file lands")

	assert.True(t, refuseRes.Refused(), "a refusing check refuses")
	assert.False(t, e.Exists(refuse, "notes.md"), "and the file does not land")
}

// T020_05: an objection and a refusal on the same event still make exactly one
// of the two outcomes.
//
// The composition case. T020_01 to T020_03 each drive a single rule, so they
// show that no ONE check can reach a third outcome. If a third outcome existed
// anywhere it would more likely appear where verdicts have to be COMBINED — one
// rule objecting at exit zero, another refusing — since that is the only place
// the engine holds two answers at once and has to reduce them.
//
// The refusal must win outright and the objection must contribute nothing: the
// work is stopped, and it is stopped for the REFUSING rule's reason. Asserting
// which reason arrives is what makes this more than "something refused" — an
// engine that merged the two, or that let the adviser's text stand in for a
// verdict, would still stop the write and would still look green without it.
//
// Both ledgers are asserted because the claim is about two rules. Two gates on
// the same event both run when the first PERMITS, so this says both were reached
// — the adviser (which permits at exit 0) is asked, and the blocker refuses.
func TestT020_05_AnObjectionCombinedWithARefusalIsJustARefusal(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "adviser", bindEveryCreate, map[string]string{
		"h.sh": `#!/bin/sh
cat >/dev/null
echo asked >> "$SR_GUARDRAIL_DIR/log"
echo '{"reason":"MARK-adviser-objects"}'
exit 0
`,
	})
	e.Gate(proj, "blocker", bindEveryCreate, map[string]string{
		"h.sh": `#!/bin/sh
cat >/dev/null
echo asked >> "$SR_GUARDRAIL_DIR/log"
echo '{"reason":"MARK-blocker-refused"}'
exit 1
`,
	})

	res := e.Run(proj, "s-020-05", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	// The adviser ran (it permits at exit 0, so it never ends the dispatch) and
	// the blocker ran (it refused). Gates run in name order between each other, so
	// "adviser" precedes "blocker" and both are reached; this says both ran, which
	// is what the composition claim needs.
	require.Equal(t, []string{"asked"}, e.GateLedgerLines(proj, "adviser", "log"),
		"the objecting rule must have been asked, or this says nothing about combining")
	require.Equal(t, []string{"asked"}, e.GateLedgerLines(proj, "blocker", "log"),
		"the refusing rule must have been asked")

	assert.True(t, res.Refused(), "a refusal combined with an objection is a refusal")
	assert.False(t, e.Exists(proj, "notes.md"),
		"and the work really is prevented — combining did not produce a third thing")
	assert.True(t, res.Saw("MARK-blocker-refused"),
		"the refusing rule's reason is what the agent is told")
	assert.False(t, res.Saw("MARK-adviser-objects"),
		"and the objection still reaches nobody — it did not become the reason by riding along")
}
