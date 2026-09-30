package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// mark_follows_completion: where a cycle's reading ended is remembered only when
// that cycle finished.
//
// The spec's reasoning: "A cycle that was interrupted may have judged nothing.
// Moving the mark anyway would skip whatever it never looked at, and nothing
// afterwards would know to go back. Re-reading a turn costs a second look;
// skipping one loses a violation for good."
//
// The asymmetry is the whole point, and it decides how these tests are built:
// the failure mode is silent. A mark moved too far produces no error, no
// refusal, and no missing output — just turns nothing ever looks at again. So
// the observation has to be "were these turns offered to a later cycle", made
// through the query a hook actually uses.
//
// impl/baseline-mark's T005_05 covers the simplest form of this by reading the
// stored mark: a session where nothing judged anything must leave no mark. What
// is NOT covered there is the consequence for a cycle that judged SOME of its
// turns and was then cut short, which is what this directory adds.

// askWhatHappened is a NEW-FORMAT file-guard (re-vehicled from the old
// GUARDRAIL.md hooks per tests/e2e/REVEHICLE-PATTERN.md), after-check so it runs
// at a cycle's end. `match: "**/*.md"` fires on whichever Post kind each cycle's
// write produced — the same two kinds the old hooks bound. The check reaches the
// session's record through SR_TRANSCRIPT / SR_WORKSPACE, which the new dispatch
// sets on a file-guard check exactly as the old-format hook env did.
const askWhatHappened = `match: "**/*.md"
checks:
  - script: ./ask.sh
`

// The check's own stdin is discarded and the record is named explicitly from the
// environment. A file-guard check is handed the flat CheckPayload rather than the
// harness payload; piping that in makes the command answer "no transcript path on
// the hook payload" every time — an answer in which every marker below reads as
// absent, indistinguishable from correct narrowing. See answered() for the guard
// that keeps that from passing. The ledger is $SR_GUARDRAIL_DIR/answers, the
// folder the engine sets for the check.
const askScript = `#!/bin/sh
cat > /dev/null
printf '{"transcript_path":"%s","cwd":"%s"}' "$SR_TRANSCRIPT" "$SR_WORKSPACE" |
  sr-session query >> "$SR_GUARDRAIL_DIR/answers" 2>&1
exit 0
`

// refusingAskScript asks the same question and then refuses for as long as the
// flag file exists.
//
// A non-zero exit is a refusal, not a crash of the engine — but from the mark's
// point of view what matters is that the cycle did not finish cleanly. This is
// the closest a test can get to an interrupted cycle through the real wiring,
// because a cycle killed outright leaves no hook to observe from. The refusal
// contract: exit non-zero refuses and a `{"reason":…}` on stdout is the reason
// the agent is told.
//
// Gated on a flag file OUTSIDE the project rather than swapped for a different
// script: the rule's own folder is what its verdicts are keyed on, so rewriting
// it between the two cycles would be a different rule, not the same rule
// finishing.
const refusingAskScript = `#!/bin/sh
cat > /dev/null
printf '{"transcript_path":"%s","cwd":"%s"}' "$SR_TRANSCRIPT" "$SR_WORKSPACE" |
  sr-session query >> "$SR_GUARDRAIL_DIR/answers" 2>&1
if [ -e "REFUSEFLAG" ]; then
  echo '{"reason":"this cycle did not finish"}'
  exit 1
fi
exit 0
`

// answered fails the test when the engine reported an error instead of entries.
//
// Every assertion here is "marker present" or "marker absent", and an error
// string satisfies "absent" perfectly. This turns that silent pass into a named
// failure.
func answered(t *testing.T, answer string) {
	t.Helper()
	if harness.EngineErrored(answer) {
		t.Fatalf("the engine reported an error instead of the session's entries, so every "+
			"marker below reads as absent and no narrowing is being tested:\n%s", answer)
	}
}

func promptsIn(answer string, markers ...string) []string {
	var found []string
	for _, m := range markers {
		if strings.Contains(answer, m) {
			found = append(found, m)
		}
	}
	return found
}

// T019_01: a cycle that did not finish does not move the mark past its turns.
//
// The first cycle's hook asks the question and then refuses, so the cycle ends
// without completing. Its turns must therefore still be available to the next
// cycle — the mark may not have advanced over work whose judging was cut short.
//
// The refusal is lifted before the second cycle so that it completes and its
// answer can be read without a refusal in the way. Both cycles write to the same
// ledger file, so the answers accumulate in order.
func TestT019_01_AnUnfinishedCycleDoesNotMoveTheMarkPastItsTurns(t *testing.T) {
	e := New(t)
	proj := e.Project()
	// A repository, so the cycle has a baseline to measure its difference
	// from. Without one there is no diff and no PostFile* event, and a rule
	// bound to one would never run.
	e.GitInit(proj)

	const sess = "s-019-01"
	const firstMarker = "MARKEREPSILON"
	const secondMarker = "MARKERZETA"

	// A cycle whose judging is cut short.
	flag := filepath.Join(t.TempDir(), "refuse")
	if err := os.WriteFile(flag, nil, 0o644); err != nil {
		t.Fatalf("write flag: %v", err)
	}
	e.FileGuard(proj, "asker", askWhatHappened, map[string]string{"ask.sh": strings.ReplaceAll(refusingAskScript, "REFUSEFLAG", flag)})
	e.CommitAll(proj, "the guards")
	first := e.Run(proj, sess, firstMarker, Turns("done",
		Write("w1", "one.md", "interrupted cycle\n"),
	).ThenCommit("the cycle"))
	// The premise: the cycle really did not finish cleanly. Without this the
	// test is about an ordinary completed cycle and proves nothing.
	//
	// Read from the blocking attachments rather than from the stream. A Post
	// hook's refusal blocks the Stop and its words travel to the agent as a
	// hook_blocking_error record in the conversation, never as a line on the
	// result stream — so Result.Saw would be asking a channel this text does not
	// use, and would fail for a working engine.
	if blocking := e.BlockingErrors(proj, sess); len(blocking) == 0 ||
		!strings.Contains(strings.Join(blocking, "\n"), "this cycle did not finish") {
		t.Fatalf("the first cycle completed normally, so there is no interrupted cycle here:\n%s\nblocking: %v",
			first.Output, blocking)
	}
	if len(e.FileGuardLedgerLines(proj, "asker", "answers")) == 0 {
		t.Fatalf("the interrupted cycle never reached the hook at all, so this proves nothing")
	}
	before := len(e.FileGuardLedgerLines(proj, "asker", "answers"))

	// The next cycle finishes cleanly, and must still be offered the turns the
	// interrupted one never settled.
	if err := os.Remove(flag); err != nil {
		t.Fatalf("lift the refusal: %v", err)
	}
	e.Run(proj, sess, secondMarker, Turns("done",
		Write("w2", "two.md", "completed cycle\n"),
	).ThenCommit("the cycle"))

	answers := e.FileGuardLedgerLines(proj, "asker", "answers")
	if len(answers) <= before {
		t.Fatalf("the second cycle never asked the engine anything (%d answers, was %d)",
			len(answers), before)
	}
	second := strings.Join(answers[before:], "\n")
	answered(t, second)

	if !strings.Contains(second, "completed cycle") {
		t.Fatalf("the second cycle was not given its own turns, so the assertion below "+
			"cannot distinguish anything:\n%s", second)
	}
	if len(promptsIn(second, firstMarker)) == 0 {
		t.Fatalf("turns from a cycle that did not finish were skipped by the next one:\n%s\n"+
			"the mark moved over work whose judging was cut short, and nothing afterwards knows "+
			"to go back — a violation there is lost for good", second)
	}
}

// T019_02: a cycle that DID finish does move the mark.
//
// The other side, and it is not optional. "Only when the cycle finished" is a
// restriction, and a build that never moved the mark at all would satisfy
// T019_01 perfectly while making the mark useless — every cycle re-reading the
// whole session, which is the waste the read-mark invariant exists to end.
//
// Two clean cycles, and the second must not be re-handed the first's turns. This
// is the same claim 018's T018_01 makes; it is repeated here because what it
// pins down in THIS directory is that completion is what licenses the advance.
// If T019_01 passes and this fails, the mark is simply never moving.
func TestT019_02_AFinishedCycleDoesMoveTheMark(t *testing.T) {
	e := New(t)
	proj := e.Project()
	// A repository, so the cycle has a baseline to measure its difference
	// from. Without one there is no diff and no PostFile* event, and a rule
	// bound to one would never run.
	e.GitInit(proj)
	e.FileGuard(proj, "asker", askWhatHappened, map[string]string{"ask.sh": askScript})
	e.CommitAll(proj, "the guards")

	const sess = "s-019-02"
	const firstMarker = "MARKERETA"
	const secondMarker = "MARKERTHETA"

	e.Run(proj, sess, firstMarker, Turns("done",
		Write("w1", "one.md", "first cycle\n"),
	).ThenCommit("the cycle"))
	before := len(e.FileGuardLedgerLines(proj, "asker", "answers"))
	if before == 0 {
		t.Fatalf("the first cycle never reached the hook, so this proves nothing")
	}

	e.Run(proj, sess, secondMarker, Turns("done",
		Write("w2", "two.md", "second cycle\n"),
	).ThenCommit("the cycle"))
	answers := e.FileGuardLedgerLines(proj, "asker", "answers")
	if len(answers) <= before {
		t.Fatalf("the second cycle never asked the engine anything (%d answers, was %d)",
			len(answers), before)
	}
	second := strings.Join(answers[before:], "\n")
	answered(t, second)

	if !strings.Contains(second, "second cycle") {
		t.Fatalf("the second cycle was not given its own turns:\n%s", second)
	}
	if len(promptsIn(second, firstMarker)) > 0 {
		t.Fatalf("a completed cycle did not move the mark: the next cycle was handed its turns "+
			"again:\n%s\nthe mark exists to stop exactly this, and a mark that never advances "+
			"leaves every cycle re-reading the whole session", second)
	}
}
