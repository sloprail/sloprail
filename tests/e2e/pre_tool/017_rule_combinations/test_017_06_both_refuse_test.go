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
// # RE-VEHICLED onto the NEW file-guard nature (was old GUARDRAIL.md hooks)
//
// Unlike T017_01..05, these three turn on the "first refusal ENDS the matter"
// short-circuit of the pre-tool point — and that short-circuit lives in the
// PREVENTIVE FILE-GUARD path (runFileGuardsPreventive returns on the first
// refusal), NOT in the gate path (which runs every matching gate and only blocks
// on the first refusal). So these use preventive file-guards, the vehicle whose
// pre-write behaviour matches the invariant: a not-fine write is refused before it
// lands, and once one guard refuses the rest are not asked. Because a permitting
// preventive guard fires BOTH at the pre-write and again at Stop (the after-check
// every guard runs), the permit-and-record check records ONLY on the PRE kinds, so
// "asked once" counts pre-write questions rather than the after-the-fact re-check.
// Refusing guards need no such filter: a PRE refusal blocks the write, so there is
// no settled file and no Stop after-check to record.

// preventiveEveryCreate is a preventive file-guard that fires on every created
// file (match: every .md path; the writes here are all .md).
const preventiveEveryCreate = `match: "**/*.md"
preventive: true
checks:
  - script: ./h.sh
`

// refusesEveryCreate is the check body of a preventive file-guard that objects to
// any creation it is shown, in words the test can attribute to it, recording that
// it ran. A PRE refusal blocks the write, so it only ever runs at the pre-write.
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
	e.FileGuard(proj, "first-rule", preventiveEveryCreate,
		map[string]string{"h.sh": refusesEveryCreate("the first rule objects")})
	e.FileGuard(proj, "second-rule", preventiveEveryCreate,
		map[string]string{"h.sh": refusesEveryCreate("the second rule objects")})

	res := e.Run(proj, "s-017-06", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	require.True(t, res.Refused(), "two rules objecting must prevent the work")
	assert.False(t, e.Exists(proj, "notes.md"),
		"the work must actually be prevented, not merely reported as refused")

	// One of the two, by name. Whichever ran first is the engine's business. The
	// file-guard attribution ("file-guard <name>") carries the folder name into
	// the refusal.
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
	e.FileGuard(proj, "first-rule", preventiveEveryCreate,
		map[string]string{"h.sh": refusesEveryCreate("the first rule objects")})
	e.FileGuard(proj, "second-rule", preventiveEveryCreate,
		map[string]string{"h.sh": refusesEveryCreate("the second rule objects")})

	res := e.Run(proj, "s-017-07", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))
	require.True(t, res.Refused(), "the write must be refused for this to be about what follows one")

	ran := len(e.FileGuardLedgerLines(proj, "first-rule", "log")) + len(e.FileGuardLedgerLines(proj, "second-rule", "log"))
	assert.Equal(t, 1, ran,
		"a pending action refused by one rule must not be put in front of the others — the "+
			"action is already prevented, and each further check is a run that buys nothing")
}

// T017_08: both rules are asked when the first one PERMITS.
//
// The control for T017_07, and it is what stops "exactly one ran" being
// satisfied by an engine that asks only ever one rule. Same two preventive
// guards, same match; the only change is that the first one permits, so the
// dispatch must continue to the second.
//
// Both permit here, so neither can end the dispatch — which makes the count a
// measurement of how many rules the engine consults rather than of where it
// stopped. The check records ONLY on the pre-write kinds, so the after-the-fact
// re-check every guard runs at Stop does not inflate the count.
func TestT017_08_BothRulesAreAskedWhenNeitherRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	// Record only when this is the PRE write (a create/update about to happen),
	// not the Stop after-check — so the count is "asked once at the pre-tool
	// point" rather than "asked at pre and again at post".
	const permitAndLog = `#!/bin/sh
p="$(cat)"
kind="$(printf '%s' "$p" | sed -n 's/.*"kind":"\([A-Za-z]*\)".*/\1/p')"
case "$kind" in
  PreFile*) echo ran >> "$SR_GUARDRAIL_DIR/log" ;;
esac
exit 0
`
	e.FileGuard(proj, "first-rule", preventiveEveryCreate, map[string]string{"h.sh": permitAndLog})
	e.FileGuard(proj, "second-rule", preventiveEveryCreate, map[string]string{"h.sh": permitAndLog})

	res := e.Run(proj, "s-017-08", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))
	require.False(t, res.Refused(), "two permitting rules must not refuse")

	assert.Len(t, e.FileGuardLedgerLines(proj, "first-rule", "log"), 1, "the first rule must be asked")
	assert.Len(t, e.FileGuardLedgerLines(proj, "second-rule", "log"), 1,
		"the second rule must be asked too when the first one permits — otherwise every rule "+
			"but one in a real project is silently disarmed")
}
