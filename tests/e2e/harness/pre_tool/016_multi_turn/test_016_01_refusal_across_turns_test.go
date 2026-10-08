// Package e2e drives enforcement across SEVERAL turns of one session.
//
// Every other pre_tool suite is one turn: an agent tries one thing and is
// refused or is not. Real sessions are not shaped like that. An agent is
// refused, changes something, and tries again; a rule passes early and the same
// file comes back much later; a rule remembers on turn 1 what it needs on turn
// 5. None of those are expressible in a single turn, and none of them were
// covered.
//
// What makes them testable here is a property of the harness worth stating
// plainly, because every test below rests on it: a session CONTINUES past a
// refusal. The mock re-runs its script after each tool result and emits the
// next turn that has not fired, so a refused turn is followed by the turn after
// it rather than ending the run. Measured, not assumed — T016_01 asserts the
// later turn's own effect on the tree, which is only observable because the
// turn happened at all.
//
// The other property these lean on: a refused Pre event leaves NOTHING on disk
// (006 pins that), so "was it refused" and "did it land" are independent
// observations. Several tests below assert both, because a rule that refuses
// the message while letting the write through is exactly the failure a
// stream-only assertion cannot see.
//
// Every rule here blocks a pending write BEFORE it lands and is re-evaluated
// fresh on each turn's write, so the vehicle is a GATE on the pre-write events.
// A gate fires once per turn's matching event (no after-check to double it) and
// is asked again every turn a matching write recurs — which is exactly the
// "asked again on the next cycle / refused again / cleared once fixed" behaviour
// this suite measures, now driven through the pre-tool gate dispatch. The checks
// read the FLAT payload; a gate's own ledger under `.sloprail/gate/<name>/`
// records each verdict. Refusals are observed with res.Saw()/Refused() and the
// tree; the ledger of verdicts is read with e.GateLedgerLines.
package e2e

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatedOnLicense refuses any .md write while the project has no LICENSE.
//
// The declaration a "fix it and retry" scenario needs: a rule whose verdict
// depends on something the agent can CHANGE between turns. A rule with a fixed
// answer could not tell a cleared refusal from a refusal that never happened.
// Triggered on both pre-write kinds, because a retry of a write that was refused
// before the file existed is a create, and a retry after it exists is an update.
const gatedOnLicense = `on:
  - event: PreFileCreate
    match: event.path endsWith ".md"
  - event: PreFileUpdate
    match: event.path endsWith ".md"
checks:
  - script: ./h.sh
`

// licenceScript refuses while LICENSE is absent, and records every question it
// was asked.
//
// The project root is reached from $SR_GUARDRAIL_DIR (the gate's own folder,
// `.sloprail/gate/<name>/`, so trimming `/.sloprail/gate/*` yields the root) —
// the new-format replacement for the old $PWD-climb and for the payload's retired
// guardrailDir field. The ledger line carries the VERDICT, not merely the fact of
// a run: "asked twice" and "refused then permitted" are different claims, and the
// second is the one a cleared refusal is about.
const licenceScript = `#!/bin/sh
cat >/dev/null
root="${SR_GUARDRAIL_DIR%/.sloprail/gate/*}"
if [ -f "$root/LICENSE" ]; then
  echo permitted >> "$SR_GUARDRAIL_DIR/log"
  exit 0
fi
echo refused >> "$SR_GUARDRAIL_DIR/log"
echo '{"reason":"this project has no LICENSE yet"}'
exit 1
`

// T016_01: a rule refuses on turn 1, the agent fixes the cause on turn 2, and
// the retry on turn 3 goes through.
//
// The scenario this suite exists for, and the one nobody had written. Three
// separate claims, each of which can fail on its own:
//
//   - the refusal reached the agent on turn 1, and nothing landed;
//   - the rule was asked AGAIN on turn 3 rather than exempted by its own earlier
//     verdict — a refusal is not a licence, but neither is it a permanent
//     sentence, and an engine caching either way gets this wrong;
//   - the retry LANDED, which is the only proof the refusal actually cleared.
//
// The last is what a stream-only assertion cannot reach. The refusal text is
// still in the output from turn 1 no matter what turn 3 did, so a test checking
// only for its absence would pass on a session where the retry was refused too.
func TestT016_01_ARefusalClearsOnceTheCauseIsFixed(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "needs-licence", gatedOnLicense, map[string]string{"h.sh": licenceScript})

	res := e.Run(proj, "s-016-01", "write docs, add a licence, write docs again", Turns("done",
		Write("t1", "README.md", "first attempt"),
		Bash("t2", "touch LICENSE"),
		Write("t3", "README.md", "second attempt"),
	))

	require.True(t, res.Saw("this project has no LICENSE yet"),
		"turn 1 must be refused, or there is no refusal for turn 3 to clear")
	require.True(t, e.Exists(proj, "LICENSE"), "turn 2's fix must have landed")

	// The verdicts in order. Two entries: refused, then permitted. Anything else
	// is a different story than the one this test claims — one entry means the
	// retry never reached the rule, and two refusals mean the fix did not clear
	// it.
	assert.Equal(t, []string{"refused", "permitted"}, e.GateLedgerLines(proj, "needs-licence", "log"),
		"the rule must be asked again after the fix, and must answer differently")

	assert.True(t, e.Exists(proj, "README.md"),
		"the retry must land — a refusal that never clears is a sentence rather than a rule")
}

// T016_02: a rule that passes on turn 1 still judges the same file on turn 3.
//
// The mirror of T016_01, and the direction an over-eager exemption breaks. A
// verdict recorded for one turn's content must not travel to a later turn's
// different content on the same path.
//
// The middle turn is deliberately an UNRELATED file. It is what makes this a
// test about turn 3 rather than about two adjacent writes: the rule sees other
// work in between, and must still be asked when the original path comes back.
//
// The content differs on the two writes to the same path, which is the whole
// point — identical content is the one case an exemption is entitled to skip,
// so using it here would make the test assert the opposite of what it names.
func TestT016_02_APassedFileIsJudgedAgainWhenItComesBack(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "watcher", gatedOnLicense, map[string]string{
		// Permits everything, and records the path it was shown. What is under
		// test is which questions the rule is asked, not how it answers them.
		"h.sh": `#!/bin/sh
p="$(cat)"
printf '%s\n' "$p" | sed 's/.*"path":"\([^"]*\)".*/\1/' >> "$SR_GUARDRAIL_DIR/log"
exit 0
`,
	})

	e.Run(proj, "s-016-02", "write, do something else, write the first file again", Turns("done",
		Write("t1", "notes.md", "version one"),
		Write("t2", "other.md", "unrelated"),
		Write("t3", "notes.md", "version two, quite different"),
	))

	assert.Equal(t, []string{"notes.md", "other.md", "notes.md"}, e.GateLedgerLines(proj, "watcher", "log"),
		"a file the rule passed early must be put back in front of it when it changes later")

	// And the last write is the one on disk. A rule being asked is worth
	// nothing if the answer did not govern the tree.
	body := readFile(t, filepath.Join(proj, "notes.md"))
	assert.Equal(t, e.Written("version two, quite different"), body,
		"the permitted rewrite must be what the tree ends up holding")
}

// T016_03: a refusal, an unrelated write, then a retry of the refused write.
//
// The interleaving T016_01 does not cover. There the fix sat between the
// refusal and the retry, so the rule's own answer changed. Here NOTHING
// relevant changes: the refused path is refused again, and the unrelated write
// in between goes through untouched.
//
// Two ways this fails. An engine that let the intervening permit clear the
// pending refusal would land the retry. An engine that let the refusal poison
// the session would refuse the unrelated write. Both are checked, because a
// test asserting only "the retry was refused" passes on an engine that refuses
// everything after the first refusal — which is a worse failure than the one it
// was written for.
func TestT016_03_ARefusalSurvivesAnUnrelatedWriteInBetween(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "guarded-dir", refuseUnderSecret, map[string]string{"h.sh": secretScript})

	res := e.Run(proj, "s-016-03", "bad, unrelated, bad again", Turns("done",
		Write("t1", "secret/keys.md", "attempt one"),
		Write("t2", "public/readme.md", "harmless"),
		Write("t3", "secret/keys.md", "attempt two"),
	))

	require.True(t, res.Saw("nothing may be written under secret/"), "the guarded path must be refused")

	assert.True(t, e.Exists(proj, "public/readme.md"),
		"an unrelated write must not be caught up in another path's refusal")
	assert.False(t, e.Exists(proj, "secret/keys.md"),
		"the retry of a refused write must be refused again while nothing has changed")

	// Asked on both attempts. An exemption granted by the intervening permit
	// would show up here as one entry rather than two — and the tree assertion
	// above would still pass, because a skipped rule permits.
	assert.Equal(t, 2, len(e.GateLedgerLines(proj, "guarded-dir", "log")),
		"both attempts on the guarded path must reach the rule")
}

// refuseUnderSecret guards one directory and leaves everything else alone.
const refuseUnderSecret = `on:
  - event: PreFileCreate
    match: event.path startsWith "secret/"
  - event: PreFileUpdate
    match: event.path startsWith "secret/"
checks:
  - script: ./h.sh
`

const secretScript = `#!/bin/sh
cat >/dev/null
echo asked >> "$SR_GUARDRAIL_DIR/log"
echo '{"reason":"nothing may be written under secret/"}'
exit 1
`
