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

// everyCreate is a gate triggering on every file creation, unnarrowed.
const everyCreate = `on:
  - event: PreFileCreate
checks:
  - script: ./h.sh
`

// T016_04: what a rule stores on turn 1 is there for it to read on turn 5.
//
// 008 proves a check can reach its own state and that it survives one cycle.
// This is the claim that actually matters for a rule that accumulates: the
// value written at the start of a session is still the value at the end of it,
// through four intervening check processes, each a separate exec.
//
// The assertion is the whole SEQUENCE, not the final count. A store that
// silently reset midway would still reach 5 if the last two turns happened to
// agree, and a store keyed per-invocation would report `prev=0` every time
// while the count came out right by accident of the arithmetic. Reading each
// turn's view of the previous one is what makes the chain unbreakable in the
// middle: every entry names both the value found and the value left, so any
// turn that lost the thread is visible as the entry where they stop lining up.
//
// A gate's check reaches `sr-session state` the same way an old hook did — the
// check env carries SR_GUARDRAIL (the gate's name, its keyspace) and
// SR_SESSION_ID — so the accumulator keys its own state under this gate.
func TestT016_04_StateWrittenOnTurnOneIsReadOnTurnFive(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Gate(proj, "accumulator", everyCreate, map[string]string{
		"h.sh": `#!/bin/sh
cat >/dev/null
prev="$(sr-session state get n 2>/dev/null)"
[ -n "$prev" ] || prev=0
n=$((prev + 1))
sr-session state set n "$n" >/dev/null 2>&1
echo "prev=$prev now=$n" >> "$SR_GUARDRAIL_DIR/log"
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
	}, e.GateLedgerLines(proj, "accumulator", "log"),
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
	e.Gate(proj, "budget", everyCreate, map[string]string{
		"h.sh": `#!/bin/sh
cat >/dev/null
prev="$(sr-session state get n 2>/dev/null)"
[ -n "$prev" ] || prev=0
n=$((prev + 1))
sr-session state set n "$n" >/dev/null 2>&1
if [ "$n" -gt 2 ]; then
  echo '{"reason":"this session may create at most two files"}'
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
// that stopped asking the permitting rule entirely once another had refused.
// The gate dispatch runs EVERY matching gate and blocks on the first refusal —
// it does not short-circuit the remaining gates the way the old hook path did —
// so the permitting rule is asked on all three turns, including the two the
// refusing rule blocks. That is a stronger form of "both keep running": the
// permitting rule's verdict is taken on every turn, and the refusing rule's
// refusal governs the outcome without retiring the other rule. Which gate is
// walked first for a given event is still the engine's business; only that both
// are asked, and the refusal governs, is pinned.
func TestT016_06_TwoRulesDisagreeAcrossTurnsAndBothKeepRunning(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "permissive", everyCreate, map[string]string{
		"h.sh": `#!/bin/sh
p="$(cat)"
printf '%s\n' "$p" | sed 's/.*"path":"\([^"]*\)".*/\1/' >> "$SR_GUARDRAIL_DIR/log"
exit 0
`,
	})
	e.Gate(proj, "no-drafts", `on:
  - event: PreFileCreate
    match: event.path startsWith "drafts/"
checks:
  - script: ./h.sh
`, map[string]string{
		"h.sh": `#!/bin/sh
p="$(cat)"
printf '%s\n' "$p" | sed 's/.*"path":"\([^"]*\)".*/\1/' >> "$SR_GUARDRAIL_DIR/log"
echo '{"reason":"drafts are not committed work"}'
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

	// The refusing rule was asked about exactly the two events its match admits,
	// and about neither of the others.
	assert.Equal(t, []string{"drafts/idea.md", "drafts/other.md"}, e.GateLedgerLines(proj, "no-drafts", "log"),
		"the narrowed rule sees only what its match admits")

	// The permitting rule kept being asked on EVERY turn — including the two the
	// refusing rule blocked and the later one after a refusal had already
	// happened. That is the claim worth pinning: a refusal does not retire the
	// other rules, and the gate dispatch consults every matching gate before it
	// blocks on the first refusal. So this rule's verdict is taken on all three
	// paths, in the order the turns ran.
	assert.Equal(t, []string{"drafts/idea.md", "final/report.md", "drafts/other.md"},
		e.GateLedgerLines(proj, "permissive", "log"),
		"a rule must still be asked on every later turn even after a different rule refused an earlier one")
}

// T016_07: a rule disabled midway through a session stops enforcing from then
// on.
//
// 011 proves a rule switched off never runs. This is the same switch thrown
// WHILE the session is running, by the agent editing the project's config — which
// is how it would actually happen, and which nothing covered.
//
// It also pins that the declarations are re-read per action rather than cached at
// session start. An engine loading once would keep enforcing the rule for the
// rest of the session, and the only visible symptom would be a rule that
// outlived being switched off.
//
// The first turn is what makes the rest non-vacuous: the rule must be shown to
// refuse before its silence afterwards means anything. Without it a misspelled
// declaration would produce the same "no refusal on turn 3" and the test would
// pass having proved nothing.
//
// A gate is disabled from the project's own `.sloprail/config.yaml`, naming it
// `gate/<name>` in `disabled:` — the consumer-side switch, distinct from the old
// format's `enabled: false` inside the declaration. The staged config is copied
// over mid-session by a Bash turn, exactly as the old test copied a disabled
// GUARDRAIL.md.
func TestT016_07_ARuleDisabledMidSessionStopsEnforcing(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "switchable", everyCreate, map[string]string{
		"h.sh": `#!/bin/sh
cat >/dev/null
echo ran >> "$SR_GUARDRAIL_DIR/log"
echo '{"reason":"this rule is in force"}'
exit 1
`,
	})

	// A config that disables the gate, staged where a shell command can copy it to
	// .sloprail/config.yaml.
	e.WriteFile(proj, "staged-config.yaml", "disabled:\n  - gate/switchable\n")
	// An EMPTY config is in place from the start, so turn 2's copy is an UPDATE of
	// an existing file rather than a create — the gate triggers only on
	// PreFileCreate, so overwriting config.yaml does not itself trip the rule that
	// is about to be switched off. (An empty `disabled:` list disables nothing.)
	e.WriteFile(proj, ".sloprail/config.yaml", "disabled: []\n")

	cfg := filepath.Join(proj, ".sloprail", "config.yaml")
	res := e.Run(proj, "s-016-07", "write, switch the rule off, write again", Turns("done",
		Write("t1", "before.md", "a"),
		// Switching a rule off is a change to the project's rules: the shipped grounded-rule-changes gate
		// matches the config at any path spelling, so the copy cites the user's own prompt.
		Bash("t2", "sr-session trajectory cite 'write, switch the rule off, write again' && cp staged-config.yaml "+shellQuote(cfg)),
		Write("t3", "after.md", "b"),
	))

	require.True(t, res.Saw("this rule is in force"),
		"the rule must refuse while it is on, or its later silence proves nothing")
	require.Contains(t, readFile(t, cfg), "gate/switchable",
		"turn 2 must actually have written the disabling config")

	assert.False(t, e.Exists(proj, "before.md"), "the write made while the rule was on is refused")
	assert.True(t, e.Exists(proj, "after.md"), "the write made after it was switched off goes through")

	assert.Equal(t, []string{"ran"}, e.GateLedgerLines(proj, "switchable", "log"),
		"a rule switched off mid-session contributes no further check runs")
}

// shellQuote renders a path as one single-quoted shell word, for a Bash turn.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
