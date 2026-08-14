package e2e

import (
	"strings"
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
// The pre-tool point stops at the first refusal — `deny` returns immediately —
// which is the opposite of the Post point, where objections are collected and
// reported together. That asymmetry is deliberate and is the reason T017_07
// exists: it pins that a pending action is refused ONCE, with one rule's reason,
// rather than being reported twice or having two reasons spliced together.

// refusesEveryCreate is a rule that objects to any file creation, in words the
// test can attribute to it.
func refusesEveryCreate(reason string) string {
	return "#!/bin/sh\ncat >/dev/null\necho ran >> \"$PWD/log\"\necho " + shq(reason) + " >&2\nexit 1\n"
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

const bindsEveryCreate = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./h.sh
---

# Objects to every file created
`

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
	e.Guardrail(proj, "first-rule", bindsEveryCreate,
		map[string]string{"h.sh": refusesEveryCreate("the first rule objects")})
	e.Guardrail(proj, "second-rule", bindsEveryCreate,
		map[string]string{"h.sh": refusesEveryCreate("the second rule objects")})

	res := e.Run(proj, "s-017-06", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	require.True(t, res.Refused(), "two rules objecting must prevent the work")
	assert.False(t, e.Exists(proj, "notes.md"),
		"the work must actually be prevented, not merely reported as refused")

	// One of the two, by name. Whichever ran first is the engine's business.
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
// matter" — observed across two GUARDRAILS rather than across two hooks of one
// binding, which is what T010_02 covers.
//
// This is the asymmetry with the Post point worth pinning. There, objections are
// collected and every rule is dispatched, because an agent fixing one violation
// per turn is the slow version of the same bug. Here there is nothing to
// collect: the action is refused and does not happen, so asking the remaining
// rules about work that is already prevented buys nothing and costs a hook run
// each — which for a model-backed judge is not free.
//
// Observed on the ledgers, since "the second rule was not asked" leaves no trace
// on the stream. Exactly one of the two rules ran: which one is not asserted.
func TestT017_07_TheFirstRefusalEndsTheMatterForAPendingAction(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "first-rule", bindsEveryCreate,
		map[string]string{"h.sh": refusesEveryCreate("the first rule objects")})
	e.Guardrail(proj, "second-rule", bindsEveryCreate,
		map[string]string{"h.sh": refusesEveryCreate("the second rule objects")})

	res := e.Run(proj, "s-017-07", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))
	require.True(t, res.Refused(), "the write must be refused for this to be about what follows one")

	ran := len(e.Ledger(proj, "first-rule", "log")) + len(e.Ledger(proj, "second-rule", "log"))
	assert.Equal(t, 1, ran,
		"a pending action refused by one rule must not be put in front of the others — the "+
			"action is already prevented, and each further hook is a run that buys nothing")
}

// T017_08: both rules are asked when the first one PERMITS.
//
// The control for T017_07, and it is what stops "exactly one ran" being
// satisfied by an engine that asks only ever one rule. Same two guardrails, same
// binding; the only change is that the first one permits, so the dispatch must
// continue to the second.
//
// Both permit here, so neither can end the dispatch — which makes the count a
// measurement of how many rules the engine consults rather than of where it
// stopped.
func TestT017_08_BothRulesAreAskedWhenNeitherRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	const permitAndLog = "#!/bin/sh\ncat >/dev/null\necho ran >> \"$PWD/log\"\nexit 0\n"
	e.Guardrail(proj, "first-rule", bindsEveryCreate, map[string]string{"h.sh": permitAndLog})
	e.Guardrail(proj, "second-rule", bindsEveryCreate, map[string]string{"h.sh": permitAndLog})

	res := e.Run(proj, "s-017-08", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))
	require.False(t, res.Refused(), "two permitting rules must not refuse")

	assert.Len(t, e.Ledger(proj, "first-rule", "log"), 1, "the first rule must be asked")
	assert.Len(t, e.Ledger(proj, "second-rule", "log"), 1,
		"the second rule must be asked too when the first one permits — otherwise every rule "+
			"but one in a real project is silently disarmed")
}
