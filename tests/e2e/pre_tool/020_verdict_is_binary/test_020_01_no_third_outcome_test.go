// Package e2e drives the one enforcement invariant nothing referenced.
//
// verdict_is_binary: "A Hook either refuses the work or permits it, with no
// third outcome that records an objection while allowing the work to proceed."
// Why: "An advisory tier is where rules go to be ignored. A rule worth
// declaring is worth enforcing, and a warning an agent may disregard is
// indistinguishable from no rule at all."
//
// An audit of enforcement.tsp found this the only one of its eleven invariants
// with no test anywhere in the tree — by name or by property. The behaviour IS
// implemented, and structurally so: `verdict` in session_pre_tool.go carries a
// bool, and its comment already explains why refusal is a field rather than a
// non-empty reason string. There is no advisory branch to find. But "the code
// has no third state" is a claim about the code, and the invariant is a claim
// about what a HOOK AUTHOR can achieve — which is a different question, because
// a hook is an arbitrary program free to print whatever it likes.
//
// So these tests come at it from the author's side. Each one is a hook
// genuinely TRYING to record an objection while letting the work through, using
// a vocabulary that exists somewhere in the hook ecosystem, and each asserts the
// attempt collapses into one of the two outcomes there are.
//
// The measured channel table in refuseForBroken is what makes this sharp rather
// than pedantic. At PreToolUse only stderr-at-exit-2 and permissionDecision
// "deny"-at-exit-0 reach the agent at all, and BOTH refuse. There is no channel
// that delivers text beside a permitted action. An advisory tier is therefore
// not merely disallowed by this engine — it is unreachable through this hook
// point, and a hook attempting one produces silence. That is the failure the
// `why` describes, and T020_01 is the test that shows it happening.
//
// # On the "no test anywhere" finding above, which was later made twice
//
// This package is the test that finding said was missing. A LATER audit reached
// the same "zero coverage anywhere, by name or by property" conclusion and acted
// on it, adding a second package — tests/e2e/pre_tool/016_verdict_is_binary —
// for this same invariant under this same name. It had missed a directory
// literally called 020_verdict_is_binary.
//
// That package is now removed. It was the weaker of the two: none of its tests
// carried a ledger, so its permit-side cases were satisfied by a guardrail that
// never loaded, and it never asserted the half that gives this invariant its
// force — that the objection reaches nobody. Its one case with no counterpart
// here, an objection combined with a refusal, is T020_05 below.
//
// Anything auditing this invariant's coverage should find it here and stop.
package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bindEveryCreate binds one hook to every file creation. What varies across
// these tests is only what the hook says and how it exits.
const bindEveryCreate = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./h.sh
---

# Sees every creation

The tests here vary how a hook tries to express a third outcome, never whether
it was asked.
`

// T020_01: a hook that says "block" and exits zero does NOT block.
//
// The purest attempt at the forbidden third outcome, and the one a hook author
// is most likely to write by accident: the refusal vocabulary the engine really
// does understand, paired with a success exit. If anything were going to record
// an objection while permitting the work, it would be this.
//
// It permits, and — the half that matters for the `why` — the objection reaches
// nobody. The reason text is not on the stream, because no channel out of a
// PreToolUse hook delivers text beside a permitted action. So the rule did not
// become advisory; it became silent, which is what "indistinguishable from no
// rule at all" means concretely.
//
// The MARK assertion is what stops this being vacuous. Asserting only that the
// write landed would pass on an engine that had never run the hook, and the
// ledger below is what separates those: the rule WAS asked, said its piece, and
// its piece went nowhere.
func TestT020_01_AnObjectionAtExitZeroPermitsAndIsNotDelivered(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "objector", bindEveryCreate, map[string]string{
		"h.sh": `#!/bin/sh
cat >/dev/null
echo asked >> "$PWD/log"
echo '{"decision":"block","reason":"MARK-I-object-but-carry-on"}'
exit 0
`,
	})

	res := e.Run(proj, "s-020-01", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	// The rule really was consulted. Without this the two assertions below are
	// satisfied by a guardrail that never loaded.
	require.Equal(t, []string{"asked"}, e.Ledger(proj, "objector", "log"),
		"the hook must have run, or nothing here is about what it decided")

	assert.False(t, res.Refused(), "exit zero permits, whatever the hook printed")
	assert.True(t, e.Exists(proj, "notes.md"),
		"the work proceeds — there is no outcome that objects and still allows it")
	assert.False(t, res.Saw("MARK-I-object-but-carry-on"),
		"and the objection reaches nobody: no channel delivers text beside a permitted action")
}

// T020_02: `permissionDecision: "ask"` is not a third outcome either.
//
// The other vocabulary an author might reach for, and the one that looks most
// like a legitimate middle tier — it is a real value in the hook protocol this
// engine's own deny() writes into. The measured table records it as reaching
// nobody and refusing nothing, and this drives that end to end.
//
// Distinct from T020_01 rather than a duplicate of it: that one is the engine's
// own refusal vocabulary at the wrong exit status, this one is a decision value
// that MEANS "neither yes nor no". If any input could produce a third outcome
// it would be the one whose entire purpose is to be a third outcome.
func TestT020_02_AskIsNeitherARefusalNorDelivered(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "asker", bindEveryCreate, map[string]string{
		"h.sh": `#!/bin/sh
cat >/dev/null
echo asked >> "$PWD/log"
echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"MARK-please-confirm-this"}}'
exit 0
`,
	})

	res := e.Run(proj, "s-020-02", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	require.Equal(t, []string{"asked"}, e.Ledger(proj, "asker", "log"),
		"the hook must have run")

	assert.False(t, res.Refused(), `"ask" does not refuse`)
	assert.True(t, e.Exists(proj, "notes.md"), "the work proceeds")
	assert.False(t, res.Saw("MARK-please-confirm-this"),
		`"ask" does not reach the agent either — it is silence, not a middle tier`)
}

// T020_03: a hook claiming approval while exiting non-zero is still a refusal.
//
// The mirror image, and the one that pins which side wins when a hook
// contradicts itself. An engine reading the printed decision in preference to
// the exit status would let a hook approve work by saying so — and since a
// non-zero exit is how every BROKEN hook fails, that would hand every crashing
// script a way to claim consent.
//
// The exit status governs, and the printed reason is used only as the wording.
// Both halves are asserted: the work was stopped, and the agent was told
// something rather than nothing.
func TestT020_03_AClaimOfApprovalDoesNotSurviveANonZeroExit(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "contradictor", bindEveryCreate, map[string]string{
		"h.sh": `#!/bin/sh
cat >/dev/null
echo asked >> "$PWD/log"
echo '{"decision":"approve","reason":"MARK-approved-anyway"}'
exit 1
`,
	})

	res := e.Run(proj, "s-020-03", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	require.Equal(t, []string{"asked"}, e.Ledger(proj, "contradictor", "log"),
		"the hook must have run")

	assert.True(t, res.Refused(),
		"a non-zero exit refuses, whatever the hook claimed about approving")
	assert.False(t, e.Exists(proj, "notes.md"),
		"and the work really is prevented, not merely reported as refused")
	assert.True(t, res.Saw("MARK-approved-anyway"),
		"the hook's own words become the reason — the status decides, the text explains")
}

// T020_04: the two outcomes are genuinely distinguishable.
//
// The control the three tests above need. Each of them asserts an attempted
// third outcome collapsed to permit or to refuse — claims that would all pass
// on an engine stuck permanently in one state. This runs the same binding with
// a plainly-permitting and a plainly-refusing hook and shows the two produce
// different answers on the tree.
//
// Without it, T020_01 and T020_02 are satisfied by an engine that permits
// everything, and T020_03 by one that refuses everything. Two of those three
// would be green on an engine with no enforcement whatsoever.
func TestT020_04_BothOutcomesAreReachable(t *testing.T) {
	e := New(t)

	permit := e.Project()
	e.Guardrail(permit, "yes", bindEveryCreate, map[string]string{
		"h.sh": "#!/bin/sh\ncat >/dev/null\nexit 0\n",
	})
	permitRes := e.Run(permit, "s-020-04a", "write", Turns("done", Write("w1", "notes.md", "hello")))

	refuse := e.Project()
	e.Guardrail(refuse, "no", bindEveryCreate, map[string]string{
		"h.sh": "#!/bin/sh\ncat >/dev/null\necho 'MARK-refused' >&2\nexit 1\n",
	})
	refuseRes := e.Run(refuse, "s-020-04b", "write", Turns("done", Write("w1", "notes.md", "hello")))

	assert.False(t, permitRes.Refused(), "a permitting hook permits")
	assert.True(t, e.Exists(permit, "notes.md"), "and the file lands")

	assert.True(t, refuseRes.Refused(), "a refusing hook refuses")
	assert.False(t, e.Exists(refuse, "notes.md"), "and the file does not land")
}

// T020_05: an objection and a refusal on the same event still make exactly one
// of the two outcomes.
//
// The composition case. T020_01 to T020_03 each drive a single rule, so they
// show that no ONE hook can reach a third outcome. If a third outcome existed
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
// Both ledgers are asserted because the claim is about two rules. Without the
// adviser's, this passes on an engine that stopped dispatching after the first
// refusal it found — which is the very short-circuit that would make the
// composition untested while the test named for it stayed green.
func TestT020_05_AnObjectionCombinedWithARefusalIsJustARefusal(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "adviser", bindEveryCreate, map[string]string{
		"h.sh": `#!/bin/sh
cat >/dev/null
echo asked >> "$PWD/log"
echo '{"decision":"block","reason":"MARK-adviser-objects"}'
exit 0
`,
	})
	e.Guardrail(proj, "blocker", bindEveryCreate, map[string]string{
		"h.sh": `#!/bin/sh
cat >/dev/null
echo asked >> "$PWD/log"
echo 'MARK-blocker-refused' >&2
exit 1
`,
	})

	res := e.Run(proj, "s-020-05", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	// Both rules were really asked. Guardrails run in no promised order between
	// each other, so this says both were reached, not which went first.
	require.Equal(t, []string{"asked"}, e.Ledger(proj, "adviser", "log"),
		"the objecting rule must have been asked, or this says nothing about combining")
	require.Equal(t, []string{"asked"}, e.Ledger(proj, "blocker", "log"),
		"the refusing rule must have been asked")

	assert.True(t, res.Refused(), "a refusal combined with an objection is a refusal")
	assert.False(t, e.Exists(proj, "notes.md"),
		"and the work really is prevented — combining did not produce a third thing")
	assert.True(t, res.Saw("MARK-blocker-refused"),
		"the refusing rule's reason is what the agent is told")
	assert.False(t, res.Saw("MARK-adviser-objects"),
		"and the objection still reaches nobody — it did not become the reason by riding along")
}
