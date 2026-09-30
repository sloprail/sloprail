package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// What a REFUSAL does to the read mark, across several cycles of one session.
//
// Two invariants meet here and neither is covered at their intersection:
//
//   - mark_follows_completion — "where a cycle's reading ended is remembered only
//     when that cycle finished". 019 covers a cycle whose hook refuses at the
//     FIRST cycle of a session, with nothing settled behind it.
//   - refusal_outlives_baseline — 015 and 024_07 cover the refusal resurfacing
//     and blocking again.
//
// What neither covers is the SEQUENCE the spec's reasoning is really about: a
// session that settles cycle one, is refused on cycle two, and comes back for
// cycle three. runPostDispatch returns false on a refusal precisely so the mark
// does not move — "the agent is about to go again over these same turns, which
// it cannot do if the cycle has just declared them judged" — so cycle three must
// be offered cycle two's span again, and must NOT be re-offered cycle one's,
// which really did finish.
//
// Both halves in one test, because they fail to opposite mutations. An engine
// that advanced the mark on a refusal loses cycle two's turns for good. An
// engine that never advanced it at all keeps handing back cycle one's, which is
// the waste the mark exists to end — and each re-read is a fresh model call free
// to reach a different verdict on work the agent can no longer reach to fix.
//
// The observation is made through `sr-session query`, the command a hook
// actually uses to ask what the agent has done. What the hook was GIVEN is what
// is read, rather than the stored mark, because the mark is bookkeeping and the
// span offered is the consequence a rule experiences.
//
// WHAT THE QUERY RETURNS, because the obvious marker does not work. It hands
// back the ASSISTANT records — the tool_use entries — and not the user turn that
// prompted them. So a cycle is identified here by the tool call it made, which
// is distinct per cycle because each writes a differently named file, rather
// than by the prompt string. A first version of this test looked for the prompt
// markers and failed against a correct engine, reporting turns as "skipped" that
// were plainly in the span.

// askAndMaybeRefuse asks the engine what the session has done, records the
// answer, and refuses when the tree holds a file the rule objects to.
//
// A NEW-FORMAT file-guard, after-check , so it runs at a cycle's end — where
// the refusal-and-read-mark interaction is measured — and its refusal RE-FIRES
// next cycle, the retained-refusal behavior this suite depends on. One rule for
// every cycle, so the refusing cycle and the clean ones write to ONE ledger in
// order: the point is a session that carries on with the same rule, and a rule
// that changed mid-session would be a second variable. `match: "**/*.md"` selects every
// markdown file in the committed changeset, whatever its status (A, M or D). The
// check refuses on a "bad" path, so the recovering cycle clears the violation by
// committing a change after which the range holds no such file (see ask.sh). The `answers` ledger has no `.md` suffix, so the
// guard is never handed its own bookkeeping.
const askAndMaybeRefuse = `match: "**/*.md"
checks:
  - script: ./ask.sh
`

// askScript records the span it was handed, then refuses if the event names a
// path containing "bad".
//
// The record is named explicitly from the environment rather than taken from the
// check's stdin: a file-guard check is handed the flat CheckPayload, and piping
// that in makes the command answer "no transcript path on the hook payload" every
// time — an answer in which every marker below reads as absent, indistinguishable
// from correct narrowing. SR_TRANSCRIPT and SR_WORKSPACE are set on every check
// process by the new dispatch for exactly this. The ledger is
// a file outside the project.
//
// The answer is bracketed so one cycle's span can be told from the next's even
// when a cycle is driven round more than once by a block.
//
// The refusal keys on the event's own `"path"` field, NOT on "bad" appearing
// anywhere in the payload. That distinction was load-bearing under the old broad
// binding, because the `answers` ledger quoted the offending file's name and the
// guard could re-observe it. Under `match: "**/*.md"` the guard never sees its own
// `answers` at all, but keeping the `"path":"bad` match is free and keeps the
// intent legible — it is the event's own subject, whose quotes are literal, and it
// sidesteps any escaped `\"file_path\":\"bad-file.md\"` riding inside newContent.
//
// New-format refusal contract: exit non-zero refuses and a `{"reason":…}` on
// stdout is the reason the agent is told, replacing the old exit-2-with-stderr.
const askTemplate = `#!/bin/sh
payload="$(cat)"
if [ -z "${SR_TRANSCRIPT:-}" ]; then
  echo "SR_TRANSCRIPT is unset, so this hook cannot read the session's record" >> "LEDGER"
  exit 0
fi
{
  printf '<<<'
  printf '{"transcript_path":"%s","cwd":"%s"}' "$SR_TRANSCRIPT" "$SR_WORKSPACE" |
    sr-session query 2>&1 | tr -d '\n'
  printf '>>>\n'
} >> "LEDGER"
case "$payload" in
  *'"path":"bad'*) echo '{"reason":"this file is not acceptable"}'; exit 1 ;;
esac
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

// project is a repository with the asking rule committed, so the rule's own
// folder is part of the baseline rather than of every difference.
func project(t *testing.T) (*harness.Env, string, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	// The ledger is outside the repository: in the rule's own folder it would be
	// committed with the agent's work, and a rule whose folder changed forgets its
	// earlier passes.
	ledger := filepath.Join(t.TempDir(), "answers")
	e.FileGuard(proj, "asker", askAndMaybeRefuse, map[string]string{"ask.sh": strings.ReplaceAll(askTemplate, "LEDGER", ledger)})
	e.DisableShippedFileGuards(proj)
	e.CommitAll(proj, "the project before the session")
	return e, proj, ledger
}

// T027_01: a refusal on cycle two leaves cycle two's turns for cycle three,
// while cycle one's stay settled.
//
// The sequence the spec's asymmetry is about, end to end. Three cycles in one
// session, each writing a distinctively named file so its turns can be picked
// out of a span:
//
//	1  clean      — completes, so its span is judged and the mark moves over it
//	2  refused    — blocked, so the mark must NOT move over its span
//	3  clean      — must be offered cycle two's span again, and not cycle one's
//
// The offending file is REMOVED before cycle three, so cycle three can complete
// and its answer can be read without a block in the way. Removing it is also
// what makes the test's claim about the MARK rather than about the refusal
// resurfacing: with the file gone there is nothing outstanding, so anything
// cycle three is handed comes from where the reading resumed.
func TestT027_01_ARefusedCycleLeavesItsTurnsForTheNextOne(t *testing.T) {
	e, proj, ledger := project(t)

	const sess = "s-027-01"
	// Each cycle is identified by the file its tool call names, because that is
	// what the query hands back. The prompts differ too, but they are user
	// records and never appear in the span.
	const first = "one.md"
	const second = "bad-file.md"
	const third = "three.md"

	// Cycle one: clean, and it completes.
	e.Run(proj, sess, "cycle one", Turns("done",
		Write("w1", "one.md", "cycle one\n"),
	).ThenCommit("the agent's work"))
	afterFirst := len(harness.ReadLedgerLines(t, ledger))
	if afterFirst == 0 {
		t.Fatalf("the first cycle never reached the hook, so nothing here can be observed")
	}
	answered(t, strings.Join(harness.ReadLedgerLines(t, ledger), "\n"))

	// Where the mark stands after a cycle that COMPLETED. The refused cycle
	// below must leave it exactly here.
	markAfterFirst := e.Meta(proj, sess, sessionstate.MetaTranscriptRead)
	if markAfterFirst == "" {
		t.Fatalf("the completed first cycle recorded no read mark, so the comparison after " +
			"the refusal has no baseline and could not detect a move")
	}

	// Cycle two: writes a file the rule objects to, and is blocked.
	e.Run(proj, sess, "cycle two", Turns("done",
		Write("w2", "bad-file.md", "violates\n"),
	).ThenCommit("the agent's work"))
	// The premise: the cycle really did not finish. Read from the blocking
	// attachments rather than the stream — a Post hook's refusal blocks the Stop
	// and its words travel as a hook_blocking_error record, never as a line on
	// the result stream.
	if blocking := e.BlockingErrors(proj, sess); len(blocking) == 0 ||
		!strings.Contains(strings.Join(blocking, "\n"), "not acceptable") {
		t.Fatalf("the second cycle completed normally (blocking: %v), so there is no "+
			"interrupted cycle here and the mark had every right to move", blocking)
	}
	afterSecond := len(harness.ReadLedgerLines(t, ledger))
	if afterSecond <= afterFirst {
		t.Fatalf("the second cycle never reached the hook (%d answers, was %d), so it read "+
			"nothing and there is no span for the third cycle to be re-offered",
			afterSecond, afterFirst)
	}

	// THE MARK ITSELF, and this assertion is the one that discriminates.
	//
	// The span the next cycle is handed cannot settle this on its own: a Post
	// refusal blocks the Stop, so the mock drives the agent round again within
	// the same cycle, and the refused turn is re-offered by that retry whatever
	// the mark did. Measured — with the `!dispatchPostEvents` guard removed so a
	// refused cycle advances the mark like a clean one, the third cycle's span
	// STILL contained the refused turn and the span assertions below stayed
	// green, while this one fails.
	//
	// So the engine's own recorded position is read. It must still name cycle
	// one's turn: the refused cycle judged nothing it is entitled to claim, and
	// the agent is about to be sent round over exactly those turns.
	markAfterRefusal := e.Meta(proj, sess, sessionstate.MetaTranscriptRead)
	if markAfterRefusal == "" {
		t.Fatalf("no read mark was recorded at all, so there is nothing here that could have " +
			"moved and this assertion cannot fail")
	}
	if markAfterRefusal != markAfterFirst {
		t.Fatalf("a refused cycle moved the read mark (%q, was %q) — the cycle did not finish, "+
			"so it has no position to claim as judged; the agent is being sent round to fix "+
			"the very turns the mark has just moved past, and nothing will offer them again",
			markAfterRefusal, markAfterFirst)
	}

	// Cycle three: the offending file is gone, so this one completes.
	e.Run(proj, sess, "cycle three", Turns("done",
		Bash("b1", "rm bad-file.md"),
		Write("w3", "three.md", "cycle three\n"),
	).ThenCommit("the agent's work"))
	answers := harness.ReadLedgerLines(t, ledger)
	if len(answers) <= afterSecond {
		t.Fatalf("the third cycle never asked the engine anything (%d answers, was %d)",
			len(answers), afterSecond)
	}
	thirdSpan := strings.Join(answers[afterSecond:], "\n")
	answered(t, thirdSpan)

	// The control: cycle three was given its own turns. Without this both
	// assertions below are satisfied by an engine that returned nothing.
	if !strings.Contains(thirdSpan, third) {
		t.Fatalf("the third cycle was not given its own turns, so nothing below can be "+
			"distinguished:\n%s", thirdSpan)
	}

	// The invariant proper: the refused cycle's turns come back.
	if !strings.Contains(thirdSpan, second) {
		t.Fatalf("turns from a cycle that was REFUSED were skipped by the next one:\n%s\n"+
			"the mark moved over work whose judging was cut short — the agent was sent round "+
			"to fix that very work, and the turns it must correct are now behind the mark and "+
			"will never be offered again", thirdSpan)
	}

	// The restriction, without which "never advance the mark" would pass: the
	// cycle that DID finish stays settled.
	if strings.Contains(thirdSpan, first) {
		t.Fatalf("turns from a cycle that COMPLETED were handed out again:\n%s\n"+
			"a refusal in a later cycle must not un-judge an earlier one — the mark only ever "+
			"moves forward, and re-reading settled work invites a different verdict on a turn "+
			"the agent can no longer reach to fix", thirdSpan)
	}
}

// T027_02: the mark resumes from the refused span, not from the whole session.
//
// The other side of T027_01's first assertion, and it is a separate claim. That
// test shows cycle two's turns coming BACK; this shows that once cycle three
// completes, they are settled — so the refusal delayed the mark by one cycle
// rather than disabling it.
//
// An engine that simply never advanced the mark after any refusal passes
// T027_01 perfectly and fails here: cycle four would be handed cycle two's and
// cycle three's turns all over again, and every cycle after that would carry
// more, which is the compounding the mark exists to end.
func TestT027_02_OnceTheRefusedSpanIsJudgedItStaysJudged(t *testing.T) {
	e, proj, ledger := project(t)

	const sess = "s-027-02"
	// Named by the tool call each cycle makes, for the reason given in T027_01.
	const refused = "bad-file.md"
	const recovered = "two.md"
	const later = "three.md"

	// A cycle that is refused.
	e.Run(proj, sess, "the refused cycle", Turns("done",
		Write("w1", "bad-file.md", "violates\n"),
	).ThenCommit("the agent's work"))
	if blocking := e.BlockingErrors(proj, sess); len(blocking) == 0 {
		t.Fatalf("the first cycle was not refused, so there is no held mark under test here")
	}

	// A cycle that clears the violation and completes. This is the one that is
	// entitled to claim the held span as judged.
	e.Run(proj, sess, "the recovering cycle", Turns("done",
		Bash("b1", "rm bad-file.md"),
		Write("w2", "two.md", "recovered\n"),
	).ThenCommit("the agent's work"))
	afterRecovered := len(harness.ReadLedgerLines(t, ledger))
	if afterRecovered == 0 {
		t.Fatalf("the recovering cycle never reached the hook, so nothing can be observed")
	}

	// A further clean cycle, which must be offered neither of the two before it.
	e.Run(proj, sess, "a later cycle", Turns("done",
		Write("w3", "three.md", "later\n"),
	).ThenCommit("the agent's work"))
	answers := harness.ReadLedgerLines(t, ledger)
	if len(answers) <= afterRecovered {
		t.Fatalf("the later cycle never asked the engine anything (%d answers, was %d)",
			len(answers), afterRecovered)
	}
	span := strings.Join(answers[afterRecovered:], "\n")
	answered(t, span)

	// The control.
	if !strings.Contains(span, later) {
		t.Fatalf("the later cycle was not given its own turns:\n%s", span)
	}

	if strings.Contains(span, refused) {
		t.Fatalf("the refused cycle's turns were handed out a THIRD time:\n%s\n"+
			"a completed cycle judged that span, so the mark should have moved over it — an "+
			"engine that stops advancing after any refusal re-reads a growing pile for the "+
			"rest of the session, which is exactly the compounding the mark exists to end", span)
	}
	if strings.Contains(span, recovered) {
		t.Fatalf("the recovering cycle's own turns were handed out again:\n%s\n"+
			"that cycle completed, so its reading is settled", span)
	}
}
