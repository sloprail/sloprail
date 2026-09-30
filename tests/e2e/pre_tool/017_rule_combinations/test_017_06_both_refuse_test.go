package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two rules that BOTH refuse the same event.
//
// T017_04 covers one refusing and one permitting, which establishes that a
// permit cannot cancel a refusal. The both-refuse case is a different question
// and was not covered: what the agent is told when two rules object to one
// pending action.
//
// The spec settles what must happen and declines to settle the rest, and these
// tests are written to that line. "Within one Binding, Hooks run in the order
// they are declared; between Guardrails, no order is promised" — so which of two
// guardrails is consulted first is deliberately not asserted anywhere below. What
// IS asserted is that the work is prevented, and that whichever refusal travels
// names the rule that produced it, because a refusal an agent cannot trace to a
// rule is not actionable.
//
// The pre-tool point stops at the first refusal — the dispatch returns
// immediately — which is the opposite of the Post point, where objections are
// collected and reported together. That asymmetry is deliberate and is the reason
// T017_07 exists: it pins that a pending action is refused ONCE, with one rule's
// reason, rather than being reported twice or having two reasons spliced together.
//
// # Vehicle: PreFileWrite gates
//
// Unlike T017_01..05, these three turn on the "first refusal ENDS the matter"
// short-circuit of the pre-tool point: a file already refused by a gate is not
// asked of the gates after it, so the pending action is refused once, before it
// lands. The vehicle is a set of gates triggering on the same PreFileWrite event.
// A gate runs once per matching pre file event, so a permit-and-record check
// records exactly one line per question — there is no settled-file re-check to
// filter out.

// gateEveryCreate is a gate that fires on every created or updated markdown file
// (the writes here are all .md).
const gateEveryCreate = `on:
  - event: PreFileWrite
    match: event.path endsWith ".md"
checks:
  - script: ./h.sh
`

// refusesEveryCreate is the check body of a gate that objects to any creation it
// is shown, in words the test can attribute to it, recording that it ran.
func refusesEveryCreate(reason string) string {
	return "#!/bin/sh\ncat >/dev/null\necho ran >> \"$SR_GUARDRAIL_DIR/log\"\necho '{\"reason\":\"" + reason + "\"}'\nexit 1\n"
}

// T017_06: when two rules both refuse, the work is prevented and the agent is
// told by name.
//
// The claims are the ones that hold whichever rule is walked first:
//
//   - nothing lands;
//   - a refusal reaches the agent;
//   - the refusal names a guardrail — and specifically one of THESE two, not a
//     generic "blocked", because an agent told only that it was blocked cannot
//     find the rule it broke.
//
// Which name arrives is not asserted. The spec promises no order between
// guardrails, so a test pinning that would be asserting something the engine is
// free to change for a good reason.
func TestT017_06_TwoRefusalsPreventTheWorkAndNameARule(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "first-rule", gateEveryCreate,
		map[string]string{"h.sh": refusesEveryCreate("the first rule objects")})
	e.Gate(proj, "second-rule", gateEveryCreate,
		map[string]string{"h.sh": refusesEveryCreate("the second rule objects")})

	res := e.Run(proj, "s-017-06", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	require.True(t, res.Refused(), "two rules objecting must prevent the work")
	assert.False(t, e.Exists(proj, "notes.md"),
		"the work must actually be prevented, not merely reported as refused")

	// One of the two, by name. Whichever ran first is the engine's business. The
	// gate attribution ("gate <name>") carries the folder name into the refusal.
	named := res.Saw("first-rule") || res.Saw("second-rule")
	assert.True(t, named,
		"the refusal must name the guardrail that produced it — an agent told only that it "+
			"was blocked cannot find the rule it broke:\n%s", res.Output)

	// And the words are a rule's own, not a synthesised summary.
	spoke := res.Saw("the first rule objects") || res.Saw("the second rule objects")
	assert.True(t, spoke,
		"the refusing rule's own reason must travel, or the agent has nothing to act on:\n%s",
		res.Output)
}

// T017_07: a pending action is refused once, and the second rule is not asked.
//
// The pre-tool point's own rule — "the first that refuses the work ends the
// matter" — observed across two GUARDRAILS.
//
// This is the asymmetry with the Post point worth pinning. There, objections are
// collected and every rule is dispatched, because an agent fixing one violation
// per turn is the slow version of the same bug. Here there is nothing to
// collect: the action is refused and does not happen, so asking the remaining
// rules about work that is already prevented buys nothing and costs a check run
// each — which for a model-backed judge is not free.
//
// Observed on the ledgers, since "the second rule was not asked" leaves no trace
// on the stream. Exactly one of the two rules ran: which one is not asserted.
func TestT017_07_TheFirstRefusalEndsTheMatterForAPendingAction(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "first-rule", gateEveryCreate,
		map[string]string{"h.sh": refusesEveryCreate("the first rule objects")})
	e.Gate(proj, "second-rule", gateEveryCreate,
		map[string]string{"h.sh": refusesEveryCreate("the second rule objects")})

	res := e.Run(proj, "s-017-07", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))
	require.True(t, res.Refused(), "the write must be refused for this to be about what follows one")

	ran := len(e.GateLedgerLines(proj, "first-rule", "log")) + len(e.GateLedgerLines(proj, "second-rule", "log"))
	assert.Equal(t, 1, ran,
		"a pending action refused by one rule must not be put in front of the others — the "+
			"action is already prevented, and each further check is a run that buys nothing")
}

// T017_08: both rules are asked when the first one PERMITS.
//
// The control for T017_07, and it is what stops "exactly one ran" being
// satisfied by an engine that asks only ever one rule. Same two gates, same
// match; the only change is that the first one permits, so the
// dispatch must continue to the second.
//
// Both permit here, so neither can end the dispatch — which makes the count a
// measurement of how many rules the engine consults rather than of where it
// stopped. Each gate records once per question, so the count is exact.
func TestT017_08_BothRulesAreAskedWhenNeitherRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	// Record each question asked at the pre-tool point.
	const permitAndLog = `#!/bin/sh
cat >/dev/null
echo ran >> "$SR_GUARDRAIL_DIR/log"
exit 0
`
	e.Gate(proj, "first-rule", gateEveryCreate, map[string]string{"h.sh": permitAndLog})
	e.Gate(proj, "second-rule", gateEveryCreate, map[string]string{"h.sh": permitAndLog})

	res := e.Run(proj, "s-017-08", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))
	require.False(t, res.Refused(), "two permitting rules must not refuse")

	assert.Len(t, e.GateLedgerLines(proj, "first-rule", "log"), 1, "the first rule must be asked")
	assert.Len(t, e.GateLedgerLines(proj, "second-rule", "log"), 1,
		"the second rule must be asked too when the first one permits — otherwise every rule "+
			"but one in a real project is silently disarmed")
}
