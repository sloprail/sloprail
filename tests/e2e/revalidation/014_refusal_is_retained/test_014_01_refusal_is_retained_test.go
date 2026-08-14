package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// refusal_is_retained: a check whose verdict is false is kept rather than
// discarded.
//
// A refusal that is forgotten stops being enforced. Keeping it is what makes
// the violation resurface every cycle until the content changes or the hook
// permits it — so what is observable from outside is not the row but its
// consequence: the hook is asked AGAIN about content it already refused, and
// refuses again.
//
// Note what a dropped refusal would look like. It does not look like a bug in
// the cycle where it happens: a refusal denies immediately either way, so that
// cycle is identical. The cost lands a cycle later — with no row, the content
// is unjudged rather than refused, and the first thing to record a pass for it
// is believed. That is why every test here spans at least two offers of the
// same content.
//
// # What is and is not covered here, and why
//
// The RETENTION ITSELF is not observable through THESE fixtures, and saying so
// is more useful than a test that appears to cover it. Every test in this
// directory observes the engine through sessionstate.Skippable, which asks "may
// this be skipped": it answers false for a retained refusal (passed is false)
// and false for a discarded one (no row at all). The two are indistinguishable
// from here however many cycles a test spans.
//
// This was measured, not assumed. Making Record drop failing verdicts outright
// leaves every test in this file green, including a flaky-judge sequence built
// specifically to make a pass compete with an earlier refusal for one row.
//
// Telling them apart needs a different question — "what is still unfixed" —
// which is sessionstate.OutstandingRefusals, read at the end of a cycle by
// readdOutstanding. That reader exists now, and the claim it enables is pinned
// end to end by T015_04 in tests/e2e/session/015_refusal_outlives_baseline: a
// refused file that has dropped out of the difference is put in front of its
// rule again. It lives there rather than here because it needs the Post kinds
// and a branch switch, neither of which this directory's fixtures have; see
// T014_05 below.
//
// What these tests pin is the OBSERVABLE half from the exemption side: a
// refusal never licenses a skip, on every subsequent offer, for as long as the
// content stays as it is. That half is real, it can fail, and it is what
// T013_01 exercises.

const judgeDecl = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./judge.sh
  PreFileUpdate:
    - hooks:
        - type: command
          command: ./judge.sh
---

# Refuses content holding a secret, and records every time it is asked.
`

// judgeScript refuses any content holding SECRET, and records what it saw so a
// test can tell an invocation that judged the pending bytes from one that
// judged whatever happened to be on disk.
const judgeScript = `#!/bin/sh
payload=$(cat)
path=$(printf '%s' "$payload" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')
ws=$(printf '%s' "$payload" | sed -n 's|.*"guardrailDir":"\(.*\)/\.sloprail/guardrails/.*|\1|p')
echo "asked disk=[$(cat "$ws/$path" 2>/dev/null)]" >> "$PWD/log"
body=$(printf '%s' "$payload" | grep -o '"content":"[^"]*"' || true)
if [ -z "$body" ]; then
  body=$(cat "$ws/$path" 2>/dev/null || true)
fi
case "$body" in
  *SECRET*) echo "content holds a secret" >&2; exit 2 ;;
esac
exit 0
`

// T014_01: a refusal re-fires on every subsequent offer of the same content.
//
// The agent offers failing content three times running. Each offer must be
// refused: the stored verdict is a refusal, so it never satisfies the passing
// half of the exemption, and the hook is asked every time.
//
// Three rather than two on purpose. Two would be satisfied by an engine that
// retains a refusal for exactly one cycle — which is what a store that
// overwrote the row with each new verdict, or expired it, would do. The third
// offer is what distinguishes "retained" from "remembered once".
func TestT014_01_ARefusalRefiresEveryCycleUntilTheContentChanges(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	got := e.Run(proj, "s-014-01", "offer the same bad content three times", Turns("done",
		Write("w1", "notes.md", "SECRET=hunter2"),
		Write("w2", "notes.md", "SECRET=hunter2"),
		Write("w3", "notes.md", "SECRET=hunter2"),
	))

	lines := e.Ledger(proj, "judge", "log")
	if n := len(lines); n != 3 {
		t.Fatalf("the guardrail was asked %d time(s), want 3 — a refusal is retained, so the "+
			"same failing content must be judged again on every offer. A dropped refusal shows "+
			"up here as a later offer being skipped, at which point the violation has gone "+
			"quiet. Ledger: %v", n, lines)
	}
	if n := strings.Count(got.Output, "content holds a secret"); n != 3 {
		t.Fatalf("the refusal reached the agent %d time(s), want 3 — being asked is not the "+
			"same as refusing, and it is the refusal that has to survive:\n%s", n, got.Output)
	}
}

// T014_02: a refusal does not become a pass when the content is fixed and then
// put back.
//
// The sequence the invariant is really about, and the one an engine gets wrong
// by keying too loosely:
//
//	SECRET=hunter2  -> refused. Nothing lands.
//	benign          -> judged, passes. This content is now settled.
//	SECRET=hunter2  -> the ORIGINAL failing content, offered again.
//
// The third offer must be refused. It is the same bytes that were refused the
// first time, and a refusal is retained — so it is neither exempt (the stored
// verdict for those bytes is a refusal) nor covered by the pass in between
// (that pass belongs to different bytes).
//
// The failure this catches is an engine keying its verdict on the PATH rather
// than on the content: the benign pass would then be the file's current verdict
// and the return to the failing content would ride it. That is the same shape
// as the bypass in 013, arrived at by going backwards instead of forwards.
//
// Every offer here is a creation, because a refused write never lands and so
// the file is never there — which is what keeps the pending bytes on the event
// where the hook can see them. See T014_03 for the same claim on the update
// path, and why it cannot yet be made there.
func TestT014_02_EditingAwayAndBackDoesNotClearTheRefusal(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	// The benign write in the middle lands, so it is removed before the third
	// offer — otherwise that offer would be an update, whose pending content
	// the hook cannot see (T014_03), and the test would be measuring the wrong
	// thing.
	got := e.Run(proj, "s-014-02", "bad, good, bad again", Turns("done",
		Write("w1", "notes.md", "SECRET=hunter2"),
		Write("w2", "notes.md", "benign"),
		Bash("b1", "rm -f notes.md"),
		Write("w3", "notes.md", "SECRET=hunter2"),
	))

	lines := e.Ledger(proj, "judge", "log")
	if n := len(lines); n != 3 {
		t.Fatalf("the guardrail was asked %d time(s), want 3 — the first offer is refused, the "+
			"second passes, and the return to the refused content must be judged in its own "+
			"right. Ledger: %v\n%s", n, lines, got.Output)
	}

	// Two refusals, not one: the first offer and the return to it. A single
	// refusal means the third offer rode the benign pass — the refusal was not
	// retained against those bytes, or was keyed on the path and overwritten.
	if n := strings.Count(got.Output, "content holds a secret"); n != 2 {
		t.Fatalf("the guardrail refused %d time(s), want 2 — content that was refused, fixed, "+
			"and then restored must be refused again. One refusal here means the pass earned "+
			"by the benign content in between was read as the file's verdict, and the "+
			"violation came back unnoticed.\nLedger: %v\n%s", n, lines, got.Output)
	}

	// And the benign write really did pass, or the two refusals above could be
	// the first two offers with the third never judged at all.
	if !got.Saw("File written successfully") {
		t.Fatalf("no write ever landed, so the benign offer never passed and the sequence "+
			"under test did not happen:\n%s", got.Output)
	}
}

// T014_03: the same claim as T014_02, on the update path.
//
// SKIPPED, and the skip lifts by deleting one line. PreFileUpdate is declared
// with a path and NO content — internal/filemod/module.go:70, against
// PreFileCreate at :64 which does carry it. So a hook bound to an update
// cannot see the payload it is being asked to permit, and reading the file
// gets the bytes the write would REPLACE.
//
// That makes this test unwritable rather than merely awkward. The sequence is
// refuse / fix / restore against a file that EXISTS throughout, so every offer
// is an update — and on every one of them the judge would be shown the
// previous write's content instead of the pending one. It would refuse and
// pass at the wrong moments, and any assertion made about it would be
// measuring the hole rather than the invariant.
//
// Written out rather than omitted so the coverage claim is honest: this half of
// refusal_is_retained is NOT covered today. When PreFileUpdate carries content,
// delete the t.Skip and this starts asserting.
func TestT014_03_EditingAwayAndBackOnTheUpdatePath(t *testing.T) {
	t.Skip("PreFileUpdate declares a path and no content (internal/filemod/module.go:70), so a " +
		"hook bound to it cannot see the pending payload — it reads the bytes the write would " +
		"replace. The refuse/fix/restore sequence against an existing file therefore cannot be " +
		"judged at all on this engine. Delete this line when the kind carries content.")

	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	// The file exists before the first offer, so all three writes are updates.
	got := e.Run(proj, "s-014-03", "bad, good, bad again in place", Turns("done",
		Bash("b0", "printf 'starting point' > notes.md"),
		Write("w1", "notes.md", "SECRET=hunter2"),
		Write("w2", "notes.md", "benign"),
		Write("w3", "notes.md", "SECRET=hunter2"),
	))

	lines := e.Ledger(proj, "judge", "log")
	if n := len(lines); n != 3 {
		t.Fatalf("the guardrail was asked %d time(s), want 3. Ledger: %v", n, lines)
	}
	if n := strings.Count(got.Output, "content holds a secret"); n != 2 {
		t.Fatalf("the guardrail refused %d time(s), want 2 — content refused, fixed, and "+
			"restored must be refused again.\nLedger: %v\n%s", n, lines, got.Output)
	}
}

// T014_04: a refusal survives alongside a pass for a DIFFERENT file.
//
// The narrow failure this catches: a store keeping one verdict per session, or
// per guardrail, rather than per file. Such a store would let the passing file
// overwrite the refused one's row, and the violation would go quiet on the next
// offer.
//
// Two files, one refused and one passing, then the refused content offered
// again. The refusal must still be there.
func TestT014_04_ARefusalSurvivesAPassForAnotherFile(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	got := e.Run(proj, "s-014-04", "one bad file, one good file, the bad one again", Turns("done",
		Write("w1", "bad.md", "SECRET=hunter2"),
		Write("w2", "good.md", "benign"),
		Write("w3", "bad.md", "SECRET=hunter2"),
	))

	lines := e.Ledger(proj, "judge", "log")
	if n := len(lines); n != 3 {
		t.Fatalf("the guardrail was asked %d time(s), want 3 — a pass recorded for one file "+
			"must not settle another. Ledger: %v", n, lines)
	}
	if n := strings.Count(got.Output, "content holds a secret"); n != 2 {
		t.Fatalf("the guardrail refused %d time(s), want 2 — the refusal on bad.md must "+
			"survive the pass recorded for good.md.\nLedger: %v\n%s", n, lines, got.Output)
	}
}

// T014_05: a refusal is distinguishable from never having been judged.
//
// The half of refusal_is_retained the tests above cannot reach, and it is no
// longer unreachable. It used to be: the only reader of a stored verdict was
// sessionstate.Skippable, which answers false for a refusal (Passed is false)
// and false for a missing row (not found) — so retaining a refusal and
// discarding one produced identical behaviour everywhere the engine looked.
//
// What closed it is sessionstate.OutstandingRefusals, the end-of-cycle reader
// that asks "what is still unfixed" rather than "may this be skipped". The
// dispatcher puts every path it names back into the cycle's difference (see
// readdOutstanding), so a violation the tree has gone quiet about is put in
// front of its rule again.
//
// The ARRANGEMENT is the whole difficulty, and getting it wrong makes this test
// pass for the wrong reason. Committing the offending file does NOT take it out
// of the difference: a cycle's difference spans committed and uncommitted work
// alike (difference_spans_both), so `git diff <baseline>` still reports it and
// the file arrives with nothing retained. The file only leaves the difference
// when the BASELINE itself moves off the history holding it — a branch switch
// onto a line that already carries the same content, which re-takes the
// measuring point and leaves the tree with nothing to say.
//
// That arrangement is exactly T015_04 in tests/e2e/session/015_refusal_outlives_
// baseline, which is where this claim is pinned end to end: it fails when
// readdOutstanding is removed and passes with it. It is not duplicated here,
// because a second copy of the same scenario in a directory whose fixtures bind
// the Pre kinds would be a weaker version of a test that already exists.
//
// What this directory keeps pinning is the observable half named at the top of
// the file: a refusal never licenses a skip, on every subsequent offer, for as
// long as the content stays as it is.
