package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// session_read_advances: a hook asking what a session has done is given the part
// of it that has not already been judged, and where that part ends is remembered
// for the next cycle to start from.
//
// The spec's reasoning is both waste and correctness: "by the twentieth cycle,
// nineteen turns are re-read every time. Worse than the waste: a judge is a model
// call rather than a function, so re-reading settled work invites a different
// verdict on a turn the agent can no longer reach to fix."
//
// The observation is made through `sr-session query`, which is the command
// a hook actually uses to ask what the agent did. A guardrail's hook script runs
// it and writes the answer to its ledger, so what the hook was GIVEN is what the
// test reads — not what the store recorded, which is the read mark's own concern
// and is covered by impl/baseline-mark.
//
// NOTE ON THE DEPENDENCIES — there are three, and each was confirmed against
// this worktree rather than assumed:
//
//  1. impl/stop-diff-impl. These hooks bind to Post kinds so they run when a
//     cycle ends, and `session stop` returns before dispatching any. Nothing
//     here runs at all without it.
//  2. impl/hook-env. A guardrail hook is handed `{event, guardrailDir}` and runs
//     with its working directory set to the guardrail's folder — no transcript
//     path, no way to name the project. `session query` therefore cannot find
//     the record; hook-env supplies SR_WORKSPACE/SR_SESSION_ID and teaches the
//     command to resolve it from them.
//  3. impl/baseline-mark. `session query` here reads the WHOLE record by design
//     and says so in its own long help — narrowing needs the remembered
//     position, which is the read-mark task.
//
// So these are written against the behaviour the spec declares. They are the
// least covered of the eight, and that is stated plainly rather than hidden
// behind a skip that looks like the others.

// askWhatHappened runs the query and records the answer, one cycle per line.
//
// A NEW-FORMAT file-guard (re-vehicled from the old GUARDRAIL.md hooks per
// tests/e2e/REVEHICLE-PATTERN.md), after-check so it runs at the END of a cycle —
// the moment the question "what has this session done since I last looked" is
// asked. `match: "**/*.md"` fires on whichever Post kind each cycle's write
// produced (create or update), the same two kinds the old hooks bound. The check
// reaches its workspace and the session's read mark exactly as the old hook did:
// the new dispatch sets SR_TRANSCRIPT / SR_WORKSPACE / SR_SESSION_ID on a
// file-guard check just as the old-format hook env did (internal/dispatch/exec.go
// mirrors services/sr-session's hookScope.env). The ledger (`answers`, no `.md`)
// is not matched, so the guard cannot re-observe its own bookkeeping.
const askWhatHappened = `match: "**/*.md"
checks:
  - script: ./ask.sh
`

// askScript records one line per invocation: the entries the engine handed back.
//
// A file-guard's check is handed the flat CheckPayload — the event and
// transcriptPath, not the harness's raw hook payload — and its working directory
// is the guard's own folder rather than the project. What lets it read the
// session's record is SR_TRANSCRIPT, which the new dispatch sets on every check
// process beside SR_GUARDRAIL, SR_SESSION_ID and SR_WORKSPACE, exactly as the
// old-format hook env did. `session query` does not read the environment itself —
// it is told which record to read, in the same payload shape a harness sends — so
// the path is passed through explicitly, the idiom the shipped example performs by
// name. Piping the check's own stdin in instead would make the command answer "no
// transcript path on the hook payload" every time, and every marker assertion
// below would then read false — which looks exactly like correct narrowing and
// would report coverage that does not exist.
//
// SR_WORKSPACE travels in the same payload as `cwd`, and it is what the
// NARROWING depends on: the read mark lives in this session's own store, and
// the store is keyed by the tree being guarded. Without it the command still
// reads the record but every cycle is handed the whole of it, which is exactly
// the assertion these tests make.
//
// SR_SESSION_ID is NOT a substitute: it carries the stable id, the uuid of the
// conversation's root record, not the transcript's filename, so a path built
// from it lands on no file. The variable is unset rather than empty when there
// is no record, so `test -n` is the whole check.
//
// The ledger is $SR_GUARDRAIL_DIR/answers — the folder the engine sets for a
// file-guard check. The guard below is what stops a silent regression: if the
// answer is an error rather than entries, the test says so instead of reading it
// as an absence.
const askScript = `#!/bin/sh
cat > /dev/null
if [ -z "${SR_TRANSCRIPT:-}" ]; then
  echo "SR_TRANSCRIPT is unset, so this hook cannot read the session's record" >> "$SR_GUARDRAIL_DIR/answers"
  exit 0
fi
printf '{"transcript_path":"%s","cwd":"%s"}' "$SR_TRANSCRIPT" "$SR_WORKSPACE" |
  sr-session query >> "$SR_GUARDRAIL_DIR/answers" 2>&1
exit 0
`

// answered fails the test when the engine reported an error instead of entries.
//
// Every assertion in this directory is of the form "marker present" or "marker
// absent", and an error string satisfies "absent" perfectly. This converts that
// silent pass into a named failure.
func answered(t *testing.T, answer string) {
	t.Helper()
	if harness.EngineErrored(answer) {
		t.Fatalf("the engine reported an error instead of the session's entries, so every "+
			"marker below reads as absent and no narrowing is being tested:\n%s", answer)
	}
}

// promptsIn reports which of the given markers appear in a recorded answer.
//
// The markers are the prompts driving each cycle, which appear in the record as
// the user turn that began them — a cheap, unambiguous way to ask "did this
// answer include cycle N's turns".
func promptsIn(answer string, markers ...string) []string {
	var found []string
	for _, m := range markers {
		if strings.Contains(answer, m) {
			found = append(found, m)
		}
	}
	return found
}

// T018_01: the second cycle is not given the first cycle's turns again.
//
// Two cycles in one session, each with a distinctive prompt. The first answer
// must contain the first cycle's marker — the control, without which "the second
// answer lacks it" is satisfied by an engine returning nothing at all. The
// second answer must contain the second cycle's marker and NOT the first's.
//
// An engine reading the whole record every time fails on the second clause,
// which is exactly the behaviour this worktree ships today.
func TestT018_01_ALaterCycleIsNotGivenAlreadyJudgedTurns(t *testing.T) {
	e := New(t)
	proj := e.Project()
	// A repository, so the cycle has a baseline to measure its difference
	// from. Without one there is no diff and no PostFile* event, and a rule
	// bound to one would never run.
	e.GitInit(proj)
	e.FileGuard(proj, "asker", askWhatHappened, map[string]string{"ask.sh": askScript})
	e.CommitAll(proj, "the guards")

	const sess = "s-018-01"
	const firstMarker = "MARKERALPHA"
	const secondMarker = "MARKERBETA"

	e.Run(proj, sess, firstMarker, Turns("done",
		Write("w1", "one.md", "first cycle\n"),
	).ThenCommit("first cycle"))
	answers := e.FileGuardLedgerLines(proj, "asker", "answers")
	if len(answers) == 0 {
		t.Fatalf("the hook never asked the engine anything, so nothing here can be observed")
	}
	answered(t, strings.Join(answers, "\n"))
	// The control: the first answer really does contain the first cycle's turns.
	if got := promptsIn(strings.Join(answers, "\n"), firstMarker); len(got) == 0 {
		t.Fatalf("the first cycle's own turns are absent from the answer it was given — "+
			"the query returned nothing recognisable, so the absence asserted below is vacuous:\n%s",
			strings.Join(answers, "\n"))
	}
	firstCount := len(answers)

	e.Run(proj, sess, secondMarker, Turns("done",
		Write("w2", "two.md", "second cycle\n"),
	).ThenCommit("second cycle"))
	answers = e.FileGuardLedgerLines(proj, "asker", "answers")
	if len(answers) <= firstCount {
		t.Fatalf("the second cycle never asked the engine anything (%d answers, was %d)",
			len(answers), firstCount)
	}
	second := strings.Join(answers[firstCount:], "\n")
	answered(t, second)

	// The second cycle's own work must be in what it was given, or the engine
	// is simply returning nothing and the real assertion cannot fail.
	if !strings.Contains(second, "second cycle") {
		t.Fatalf("the second cycle was not given its own turns:\n%s", second)
	}
	// The invariant proper.
	if len(promptsIn(second, firstMarker)) > 0 {
		t.Fatalf("the second cycle was handed turns the first cycle already judged:\n%s\n"+
			"re-reading settled work compounds every cycle, and invites a different verdict on a "+
			"turn the agent can no longer reach to fix", second)
	}
}

// T018_02: a cycle that follows one which judged nothing still sees the earlier
// turns.
//
// The boundary the mark exists to respect from the other side. Narrowing is only
// safe where something actually looked; a cycle whose turns were never judged
// must still be available to the next one. Here the first cycle has no guardrail
// bound at all — nothing judged it — and the guardrail is introduced for the
// second cycle, which must then be given the first cycle's turns rather than
// starting after them.
//
// This is what separates "remember where the reading ended" from "remember where
// the session got to". The second loses turns nothing ever judged.
func TestT018_02_TurnsNothingJudgedAreStillGivenToTheNextCycle(t *testing.T) {
	e := New(t)
	proj := e.Project()
	// A repository, so the cycle has a baseline to measure its difference
	// from. Without one there is no diff and no PostFile* event, and a rule
	// bound to one would never run.
	e.GitInit(proj)

	const sess = "s-018-02"
	const firstMarker = "MARKERGAMMA"
	const secondMarker = "MARKERDELTA"

	// No guardrail yet: nothing judges this cycle.
	e.Run(proj, sess, firstMarker, Turns("done",
		Write("w1", "one.md", "unjudged cycle\n"),
	))

	// Now a rule appears, and the next cycle asks what the session has done.
	e.FileGuard(proj, "asker", askWhatHappened, map[string]string{"ask.sh": askScript})
	e.CommitAll(proj, "the guards")
	e.Run(proj, sess, secondMarker, Turns("done",
		Write("w2", "two.md", "judged cycle\n"),
	).ThenCommit("judged cycle"))

	answers := strings.Join(e.FileGuardLedgerLines(proj, "asker", "answers"), "\n")
	if answers == "" {
		t.Fatalf("the hook never asked the engine anything, so nothing here can be observed")
	}
	answered(t, answers)
	if !strings.Contains(answers, "judged cycle") {
		t.Fatalf("the cycle was not given its own turns, so this proves nothing:\n%s", answers)
	}
	if len(promptsIn(answers, firstMarker)) == 0 {
		t.Fatalf("turns from a cycle that nothing judged were skipped:\n%s\n"+
			"the mark may only move over work something actually looked at — skipping a turn "+
			"loses a violation for good", answers)
	}
}
