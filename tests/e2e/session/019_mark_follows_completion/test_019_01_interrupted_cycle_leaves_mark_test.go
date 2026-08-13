package e2e

import (
	"strings"
	"testing"
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

const askWhatHappened = `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./ask.sh
  PostFileUpdate:
    - hooks:
        - type: command
          command: ./ask.sh
---

# Asks the engine what the session has done, and records the answer
`

// Called bare, with the payload discarded. A guardrail hook is handed
// `{event, guardrailDir}` rather than the harness payload, so it has no
// transcript_path to forward; impl/hook-env is what sets SR_WORKSPACE and
// SR_SESSION_ID on the hook process and lets `session query` resolve the record
// from them. Piping the event payload in makes the command answer "no
// transcript path on the hook payload" every time — an answer in which every
// marker below reads as absent, which is indistinguishable from correct
// narrowing. See answered() for the guard that keeps that from passing.
const askScript = `#!/bin/sh
cat > /dev/null
sloprail session query >> "$PWD/answers" 2>&1
exit 0
`

// crashingAsk asks the same question and then fails.
//
// A non-zero exit at an after-the-fact point is a refusal, not a crash of the
// engine — but from the mark's point of view what matters is that the cycle did
// not finish cleanly. This is the closest a test can get to an interrupted cycle
// through the real wiring, because a cycle killed outright leaves no hook to
// observe from.
const crashingAskScript = `#!/bin/sh
cat > /dev/null
sloprail session query >> "$PWD/answers" 2>&1
echo "this cycle did not finish" >&2
exit 2
`

// answered fails the test when the engine reported an error instead of entries.
//
// Every assertion here is "marker present" or "marker absent", and an error
// string satisfies "absent" perfectly. This turns that silent pass into a named
// failure.
func answered(t *testing.T, answer string) {
	t.Helper()
	if strings.Contains(answer, "sloprail:") {
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
// The guardrail is swapped for a clean one before the second cycle so that the
// second cycle completes and its answer can be read without a refusal in the
// way. Both write to the same ledger file, so the answers accumulate in order.
func TestT019_01_AnUnfinishedCycleDoesNotMoveTheMarkPastItsTurns(t *testing.T) {
	t.Skip("blocked on impl/stop-diff-impl (no PostFile* dispatch, so this hook never runs) and impl/hook-env (a guardrail hook gets no transcript path and no SR_WORKSPACE, so `session query` cannot find the record) and impl/baseline-mark (nothing records a read position, so there is no narrowing to observe)")
	e := New(t)
	proj := e.Project()

	const sess = "s-019-01"
	const firstMarker = "MARKEREPSILON"
	const secondMarker = "MARKERZETA"

	// A cycle whose judging is cut short.
	e.Guardrail(proj, "asker", askWhatHappened, map[string]string{"ask.sh": crashingAskScript})
	first := e.Run(proj, sess, firstMarker, Turns("done",
		Write("w1", "one.md", "interrupted cycle\n"),
	))
	// The premise: the cycle really did not finish cleanly. Without this the
	// test is about an ordinary completed cycle and proves nothing.
	if !first.Saw("this cycle did not finish") {
		t.Fatalf("the first cycle completed normally, so there is no interrupted cycle here:\n%s",
			first.Output)
	}
	if len(e.Ledger(proj, "asker", "answers")) == 0 {
		t.Fatalf("the interrupted cycle never reached the hook at all, so this proves nothing")
	}
	before := len(e.Ledger(proj, "asker", "answers"))

	// The next cycle finishes cleanly, and must still be offered the turns the
	// interrupted one never settled.
	e.Guardrail(proj, "asker", askWhatHappened, map[string]string{"ask.sh": askScript})
	e.Run(proj, sess, secondMarker, Turns("done",
		Write("w2", "two.md", "completed cycle\n"),
	))

	answers := e.Ledger(proj, "asker", "answers")
	if len(answers) <= before {
		t.Fatalf("the second cycle never asked the engine anything (%d answers, was %d)",
			len(answers), before)
	}
	second := strings.Join(answers[before:], "\n")
	answered(t, second)

	if len(promptsIn(second, secondMarker)) == 0 {
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
	t.Skip("blocked on impl/stop-diff-impl (no PostFile* dispatch, so this hook never runs) and impl/hook-env (a guardrail hook gets no transcript path and no SR_WORKSPACE, so `session query` cannot find the record) and impl/baseline-mark (nothing records a read position, so there is no narrowing to observe)")
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "asker", askWhatHappened, map[string]string{"ask.sh": askScript})

	const sess = "s-019-02"
	const firstMarker = "MARKERETA"
	const secondMarker = "MARKERTHETA"

	e.Run(proj, sess, firstMarker, Turns("done",
		Write("w1", "one.md", "first cycle\n"),
	))
	before := len(e.Ledger(proj, "asker", "answers"))
	if before == 0 {
		t.Fatalf("the first cycle never reached the hook, so this proves nothing")
	}

	e.Run(proj, sess, secondMarker, Turns("done",
		Write("w2", "two.md", "second cycle\n"),
	))
	answers := e.Ledger(proj, "asker", "answers")
	if len(answers) <= before {
		t.Fatalf("the second cycle never asked the engine anything (%d answers, was %d)",
			len(answers), before)
	}
	second := strings.Join(answers[before:], "\n")
	answered(t, second)

	if len(promptsIn(second, secondMarker)) == 0 {
		t.Fatalf("the second cycle was not given its own turns:\n%s", second)
	}
	if len(promptsIn(second, firstMarker)) > 0 {
		t.Fatalf("a completed cycle did not move the mark: the next cycle was handed its turns "+
			"again:\n%s\nthe mark exists to stop exactly this, and a mark that never advances "+
			"leaves every cycle re-reading the whole session", second)
	}
}
