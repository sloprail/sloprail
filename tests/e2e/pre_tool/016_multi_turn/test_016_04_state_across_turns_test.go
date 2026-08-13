package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// readFile returns a project file's contents, failing the test when it is not
// there. Used where the test has already asserted the file exists and wants to
// know what is in it.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

// everyCreate binds one hook to every file creation, unnarrowed.
const everyCreate = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./h.sh
---

# Sees every creation
`

// T016_04: what a rule stores on turn 1 is there for it to read on turn 5.
//
// 008 proves a hook can reach its own state and that it survives one cycle.
// This is the claim that actually matters for a rule that accumulates: the
// value written at the start of a session is still the value at the end of it,
// through four intervening hook processes, each a separate exec.
//
// The assertion is the whole SEQUENCE, not the final count. A store that
// silently reset midway would still reach 5 if the last two turns happened to
// agree, and a store keyed per-invocation would report `prev=0` every time
// while the count came out right by accident of the arithmetic. Reading each
// turn's view of the previous one is what makes the chain unbreakable in the
// middle: every entry names both the value found and the value left, so any
// turn that lost the thread is visible as the entry where they stop lining up.
func TestT016_04_StateWrittenOnTurnOneIsReadOnTurnFive(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "accumulator", everyCreate, map[string]string{
		"h.sh": `#!/bin/sh
cat >/dev/null
prev="$(sloprail session state get n 2>/dev/null)"
[ -n "$prev" ] || prev=0
n=$((prev + 1))
sloprail session state set n "$n" >/dev/null 2>&1
echo "prev=$prev now=$n" >> "$PWD/log"
exit 0
`,
	})

	e.Run(proj, "s-016-04", "five writes", Turns("done",
		Write("t1", "a.md", "1"),
		Write("t2", "b.md", "2"),
		Write("t3", "c.md", "3"),
		Write("t4", "d.md", "4"),
		Write("t5", "e.md", "5"),
	))

	assert.Equal(t, []string{
		"prev=0 now=1",
		"prev=1 now=2",
		"prev=2 now=3",
		"prev=3 now=4",
		"prev=4 now=5",
	}, e.Ledger(proj, "accumulator", "log"),
		"each turn must find what the turn before it stored")
}

// T016_05: a rule that only refuses once it has seen enough.
//
// The reason T016_04's mechanism is worth having, driven to an actual verdict.
// The rule permits the first two creations and refuses the third — a budget,
// which is a real rule shape and one that cannot be expressed at all without
// state surviving between turns.
//
// This is the test that would catch a store that reads back correctly but is
// never CONSULTED for a decision. T016_04 asserts the values travel; this
// asserts a verdict changed because of them, and that the tree reflects it.
func TestT016_05_ARuleRefusesOnlyOnceItsBudgetIsSpent(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "budget", everyCreate, map[string]string{
		"h.sh": `#!/bin/sh
cat >/dev/null
prev="$(sloprail session state get n 2>/dev/null)"
[ -n "$prev" ] || prev=0
n=$((prev + 1))
sloprail session state set n "$n" >/dev/null 2>&1
if [ "$n" -gt 2 ]; then
  echo "this session may create at most two files" >&2
  exit 1
fi
exit 0
`,
	})

	res := e.Run(proj, "s-016-05", "four writes against a budget of two", Turns("done",
		Write("t1", "one.md", "a"),
		Write("t2", "two.md", "b"),
		Write("t3", "three.md", "c"),
		Write("t4", "four.md", "d"),
	))

	require.True(t, res.Saw("this session may create at most two files"),
		"the budget must be enforced once it is spent")

	// The tree is the proof the budget was counted rather than guessed: the
	// first two landed and the last two did not. A rule refusing from the start
	// would leave nothing; one never refusing would leave all four.
	assert.True(t, e.Exists(proj, "one.md"), "the first write is within budget")
	assert.True(t, e.Exists(proj, "two.md"), "the second write is within budget")
	assert.False(t, e.Exists(proj, "three.md"), "the third write is over budget")
	assert.False(t, e.Exists(proj, "four.md"), "the fourth write is over budget")
}

// twoRules is one declaration text used for two guardrails that disagree. Their
// hooks differ; their bindings do not, so both see every creation and any
// difference in outcome is the hooks', not the bindings'.
const twoRules = everyCreate

// T016_06: two rules disagreeing about the same event, across turns.
//
// One rule permits everything. The other refuses anything under `drafts/`.
// Over three turns the pair is asked about a path the second guards, a path it
// does not, and the guarded path again.
//
// What this pins is that a refusal is not a property of the SESSION but of the
// event: the permitting rule keeps permitting, the refusing rule keeps
// refusing, and neither one's verdict leaks into the other's turns. The
// invariant says no order is promised between guardrails — so this deliberately
// asserts nothing about which ran first, only that both were asked and that the
// refusal governed.
//
// Both ledgers are read. Asserting only the outcome would pass on an engine
// that stopped asking the permitting rule entirely once another had refused,
// which is a real way for "the first refusal ends the matter" to be
// over-applied: it must end the matter for THAT event, not retire the other
// rule for the rest of the session.
func TestT016_06_TwoRulesDisagreeAcrossTurnsAndBothKeepRunning(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "permissive", twoRules, map[string]string{
		"h.sh": `#!/bin/sh
p="$(cat)"
printf '%s\n' "$p" | sed 's/.*"path":"\([^"]*\)".*/\1/' >> "$PWD/log"
exit 0
`,
	})
	e.Guardrail(proj, "no-drafts", `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "drafts/"
      hooks:
        - type: command
          command: ./h.sh
---

# Nothing under drafts/
`, map[string]string{
		"h.sh": `#!/bin/sh
p="$(cat)"
printf '%s\n' "$p" | sed 's/.*"path":"\([^"]*\)".*/\1/' >> "$PWD/log"
echo 'drafts are not committed work' >&2
exit 1
`,
	})

	res := e.Run(proj, "s-016-06", "guarded, unguarded, guarded again", Turns("done",
		Write("t1", "drafts/idea.md", "one"),
		Write("t2", "final/report.md", "two"),
		Write("t3", "drafts/other.md", "three"),
	))

	require.True(t, res.Saw("drafts are not committed work"), "the refusing rule must refuse")

	assert.False(t, e.Exists(proj, "drafts/idea.md"), "a guarded path must not land")
	assert.False(t, e.Exists(proj, "drafts/other.md"), "and must still not land on a later turn")
	assert.True(t, e.Exists(proj, "final/report.md"), "the path neither rule refuses must land")

	// The refusing rule was asked about exactly the two events its matcher
	// admits, and about neither of the others.
	assert.Equal(t, []string{"drafts/idea.md", "drafts/other.md"}, e.Ledger(proj, "no-drafts", "log"),
		"the narrowed rule sees only what its matcher admits")

	// The permitting rule kept being asked AFTER another rule refused, on a
	// later turn. That is the claim worth pinning: "the first refusal ends the
	// matter" must end it for that EVENT, not retire the other rules for the
	// rest of the session.
	//
	// It is asked about `final/report.md` and about nothing else, and the
	// absence of the two drafts paths is engine behaviour rather than an
	// oversight in this test. Dispatch returns at the first refusal, and the
	// refusing rule sorts ahead of this one — so for a refused event the
	// remaining guardrails are never reached. The invariant promises no order
	// between guardrails, which makes "who got asked before the refusal"
	// precisely the thing a test must NOT assert: it would be pinning the
	// iteration order the spec declines to promise. What is asserted is the one
	// entry the ordering cannot take away.
	assert.Equal(t, []string{"final/report.md"}, e.Ledger(proj, "permissive", "log"),
		"a rule must still be asked on a later turn after a different rule refused an earlier one")
}

// T016_07: a rule disabled midway through a session stops enforcing from then
// on.
//
// 011 proves a rule declared `enabled: false` never runs. This is the same
// switch thrown WHILE the session is running, by the agent editing the
// declaration — which is how it would actually happen, and which nothing
// covered.
//
// It also pins that the declaration is re-read per action rather than cached at
// session start. An engine loading once would keep enforcing the rule for the
// rest of the session, and the only visible symptom would be a rule that
// outlived being switched off.
//
// The first turn is what makes the rest non-vacuous: the rule must be shown to
// refuse before its silence afterwards means anything. Without it a
// misspelled declaration would produce the same "no refusal on turn 3" and the
// test would pass having proved nothing.
func TestT016_07_ARuleDisabledMidSessionStopsEnforcing(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "switchable", everyCreate, map[string]string{
		"h.sh": `#!/bin/sh
cat >/dev/null
echo ran >> "$PWD/log"
echo 'this rule is in force' >&2
exit 1
`,
	})

	// The same declaration with the switch off, staged where a shell command can
	// copy it over the original. Written from the constant rather than typed out
	// again, so the two cannot drift into being different rules.
	off := strings.Replace(everyCreate, "---\nhooks:", "---\nenabled: false\nhooks:", 1)
	require.Contains(t, off, "enabled: false", "the staged declaration must really be the disabled one")
	e.WriteFile(proj, "staged-off.md", off)

	decl := filepath.Join(proj, ".sloprail", "guardrails", "switchable", "GUARDRAIL.md")
	res := e.Run(proj, "s-016-07", "write, switch the rule off, write again", Turns("done",
		Write("t1", "before.md", "a"),
		Bash("t2", "cp staged-off.md "+shellQuote(decl)),
		Write("t3", "after.md", "b"),
	))

	require.True(t, res.Saw("this rule is in force"),
		"the rule must refuse while it is on, or its later silence proves nothing")
	require.Contains(t, readFile(t, decl), "enabled: false",
		"turn 2 must actually have disabled the declaration")

	assert.False(t, e.Exists(proj, "before.md"), "the write made while the rule was on is refused")
	assert.True(t, e.Exists(proj, "after.md"), "the write made after it was switched off goes through")

	assert.Equal(t, []string{"ran"}, e.Ledger(proj, "switchable", "log"),
		"a rule switched off mid-session contributes no further hook runs")
}

// shellQuote renders a path as one single-quoted shell word, for a Bash turn.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
