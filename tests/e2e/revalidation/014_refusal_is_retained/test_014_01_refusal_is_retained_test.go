package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// refusal_is_retained: a check whose verdict is false is kept rather than
// discarded.
//
// A refusal that is forgotten stops being enforced. Keeping it is what makes
// the violation resurface every cycle until the content changes or the check
// permits it — so what is observable from outside is not the row but its
// consequence: the check is asked AGAIN about content it already refused, and
// refuses again.
//
// Note what a dropped refusal would look like. It does not look like a bug in
// the cycle where it happens: a refusal denies immediately either way, so that
// cycle is identical. The cost lands a cycle later — with no row, the content
// is unjudged rather than refused, and the first thing to record a pass for it
// is believed. That is why every test here spans at least two offers of the
// same content.
//
// # RE-VEHICLED onto the NEW file-guard nature (was old GUARDRAIL.md hooks)
//
// This directory tests the SHARED revalidation machinery — revalidation.Record
// keeping a failing FileCheck, Skippable answering false for it, and
// readdOutstanding re-adding the still-refused path to the next cycle's
// difference. The new file-guard's AFTER-check drives that exact machinery
// (services/sr-session/nature_fileguard.go's runFileGuardsPost calls the same
// rev.Subject / rev.Skip / rev.Record the old dispatch did), so re-vehicling onto
// e.FileGuard observes the SAME retention through the NEW dispatch. The exact
// transformation is in tests/e2e/REVEHICLE-PATTERN.md.
//
// The observation channel moves with the vehicle. The old Pre-path dispatch
// refused a write BEFORE it landed and the refusal travelled back on the tool
// stream; a file-guard's after-check judges the SETTLED file, so the write lands
// and its refusal is a Stop block that re-fires next cycle. So:
//
//   - "the check was asked" is still the check's own ledger, read with
//     e.FileGuardLedgerLines — now under `.sloprail/file-guard/<name>/`.
//   - "the refusal reached the agent" is now e.BlockingErrorsFrom(…, "Stop"), the
//     format-neutral channel a Stop-blocking refusal travels down.
//   - retention is observed as re-fire ACROSS cycles rather than re-judgement of
//     successive offers within one cycle: two writes of one path in a single
//     cycle collapse to one net Post event, so the same-content offers the old
//     Pre path counted per-write are driven here as one write plus the re-fires a
//     still-not-fine file produces on later cycles — the shape 015's
//     refusal_outlives_baseline and fileguard/034_03 already pin.
//
// # What is and is not covered here, and why
//
// The RETENTION ITSELF is not observable through THESE fixtures alone, and saying
// so is more useful than a test that appears to cover it. Every test in this
// directory observes the engine through the exemption side — a refusal never
// licenses a skip, on every subsequent cycle, for as long as the content stays as
// it is. That half is real, it can fail, and it is what T013_01 exercises.
//
// Telling a retained refusal apart from a discarded one needs the reader that asks
// "what is still unfixed" — sessionstate.OutstandingRefusals, read at the end of a
// cycle by readdOutstanding — which is pinned end to end by T015_04 in
// tests/e2e/session/015_refusal_outlives_baseline (also re-vehicled onto a
// file-guard). It lives there rather than here because it needs a branch switch
// this directory's fixtures do not have.

// forbidSecretGuard is a file-guard (acts only at Stop): a `.md` file is not fine if
// its settled content holds SECRET. The after-check fires on the POST file event,
// re-fires next cycle until the file is fixed, and records every time it is ASKED
// so a test can count re-fires.
//
// `match: "**/*.md"` selects the same files the old path-based hook saw at any
// depth (the `**/` leading dir is optional), and never matches the guard's own
// `log` ledger (no `.md` suffix), so no self-observation doubles the ledger.
const forbidSecretGuard = `match: "**/*.md"
checks:
  - script: ./judge.sh
`

// judgeScript records that it was asked, then refuses when the settled file holds
// SECRET.
//
// It reads the file off DISK: a file-guard's after-check runs on the POST event,
// by which point the write has landed and the settled bytes are what a refusal
// must be about. The project root is derived from $SR_GUARDRAIL_DIR — the one
// absolute location the engine hands a file-guard check
// (`.sloprail/file-guard/<name>`, so trimming `/.sloprail/file-guard/*` yields the
// root). A prefix that resolved empty would make every grep miss and refuse
// nothing, which the refusal ledger below is what makes visible.
//
// New-format refusal contract: exit non-zero refuses, and the reason on stdout as
// `{"reason":"…"}` is what the agent is told (scriptRefusalReason prefers
// structured stdout). The reason names the PATH so distinct not-fine files produce
// distinct Stop-block strings — e.BlockingErrorsFrom de-duplicates by text, and a
// per-path reason is what lets two different files' refusals be told apart. This
// replaces the old exit-2-with-stderr channel.
//
// A defensive `.sloprail/*` skip is kept: under `**/*.md` it is not load-bearing
// (the ledgers have no `.md` suffix), but it keeps the rule about the agent's
// files rather than the engine's own bookkeeping.
const judgeScript = `#!/bin/sh
payload=$(cat)
path=$(printf '%s' "$payload" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')
case "$path" in
  .sloprail/*) exit 0 ;;
esac
echo "asked path=[$path]" >> "$SR_GUARDRAIL_DIR/log"
root="${SR_GUARDRAIL_DIR%/.sloprail/file-guard/*}"
if [ -n "$path" ] && [ -f "$root/$path" ] && grep -q SECRET "$root/$path"; then
  printf '{"reason":"content of %s holds a secret"}\n' "$path"
  exit 1
fi
exit 0
`

// asked counts, over the delta of the ledger a cycle added, how many times the
// check was invoked about a given path.
func askedAbout(lines []string, path string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, "path=["+path+"]") {
			n++
		}
	}
	return n
}

// blocks reads the distinct Stop-blocking refusals recorded for a session. A
// refusal's reason names its path, so a per-path refusal is one distinct string.
func refusedPath(t *testing.T, e *harness.Env, proj, sess, path string) bool {
	t.Helper()
	for _, b := range e.BlockingErrorsFrom(proj, sess, "Stop") {
		if strings.Contains(b, "content of "+path+" holds a secret") {
			return true
		}
	}
	return false
}

// T014_01: a refusal re-fires on every subsequent cycle until the content
// changes.
//
// The bad file is written once; it lands, is refused, and — because the refusal
// is retained — is put back in front of the check on every later cycle that does
// not fix it. Three cycles, and the bad file must be judged in each.
//
// Three rather than two on purpose. Two would be satisfied by an engine that
// retains a refusal for exactly one cycle — a store that overwrote or expired the
// row. The third cycle is what distinguishes "retained" from "remembered once".
//
// The old Pre-path form offered the same bad content three times in ONE cycle and
// counted three denies; on the after-check path two writes of one path in a cycle
// collapse to one net event, so the three offers become three CYCLES, which is the
// durable shape retention is actually about — the row survives the process
// boundary between them.
func TestT014_01_ARefusalRefiresEveryCycleUntilTheContentChanges(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "judge", forbidSecretGuard, map[string]string{"judge.sh": judgeScript})

	const sess = "s-014-01"

	// Cycle 1: the bad file lands and is refused.
	seen := 0
	e.Run(proj, sess, "write bad content", Turns("done",
		Write("w1", "notes.md", "SECRET=hunter2"),
	))
	first := e.FileGuardLedgerLines(proj, "judge", "log")
	if askedAbout(first[seen:], "notes.md") == 0 {
		t.Fatalf("the bad file was never judged in the first cycle: %v — nothing was refused, so "+
			"there is no surviving refusal to test", first)
	}
	seen = len(first)
	if !refusedPath(t, e, proj, sess, "notes.md") {
		t.Fatalf("the bad file was not refused in the first cycle, so this is not a retained refusal")
	}

	// Cycles 2 and 3: unrelated work that does NOT touch the bad file. A retained
	// refusal must put it back in front of the check each time.
	//
	// A fresh turn id AND a fresh path each cycle: a reused turn id is silently
	// skipped by the mock (its marker is already in the transcript), which would
	// read as an empty cycle, and a reused unrelated path could be legitimately
	// skipped by revalidation once it has passed.
	for cycle := 2; cycle <= 3; cycle++ {
		e.Run(proj, sess, "unrelated work", Turns("done",
			Write(fmt.Sprintf("u%d", cycle), fmt.Sprintf("unrelated%d.md", cycle), "fine"),
		))
		lines := e.FileGuardLedgerLines(proj, "judge", "log")
		if n := askedAbout(lines[seen:], "notes.md"); n == 0 {
			t.Fatalf("cycle %d did not re-judge the still-not-fine file (delta %v) — a refusal is "+
				"retained, so the same failing content must be judged again on every cycle. A "+
				"dropped refusal shows up here as a later cycle skipping it, at which point the "+
				"violation has gone quiet", cycle, lines[seen:])
		}
		seen = len(lines)
	}
}

// T014_02: a refusal does not become a pass when the content is fixed and then
// put back.
//
// The sequence the invariant is really about, and the one an engine gets wrong by
// keying too loosely:
//
//	cycle 1  SECRET=hunter2  -> refused, a fail recorded on those bytes.
//	cycle 2  benign          -> passes. This content is now settled.
//	cycle 3  SECRET=hunter2  -> the ORIGINAL failing content, offered again.
//
// The third cycle must be refused. It is the same bytes that were refused the
// first time, and a refusal is retained — so it is neither exempt (the stored
// verdict for those bytes is a refusal) nor covered by the pass in between (that
// pass belongs to different bytes).
//
// The failure this catches is an engine keying its verdict on the PATH rather than
// on the content: the benign pass would then be the file's current verdict and the
// return to the failing content would ride it.
func TestT014_02_EditingAwayAndBackDoesNotClearTheRefusal(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "judge", forbidSecretGuard, map[string]string{"judge.sh": judgeScript})

	const sess = "s-014-02"

	e.Run(proj, sess, "bad", Turns("done", Write("w1", "notes.md", "SECRET=hunter2")))
	if !refusedPath(t, e, proj, sess, "notes.md") {
		t.Fatalf("the first offer was not refused, so there is no refusal to survive the fix")
	}

	// Fixed. The pass belongs to the benign bytes.
	e.Run(proj, sess, "good", Turns("done", Write("w2", "notes.md", "benign")))

	// Back to the failing content. It must be refused again — the pass in between
	// was about other bytes.
	seen := len(e.FileGuardLedgerLines(proj, "judge", "log"))
	e.Run(proj, sess, "bad again", Turns("done", Write("w3", "notes.md", "SECRET=hunter2")))
	after := e.FileGuardLedgerLines(proj, "judge", "log")
	if n := askedAbout(after[seen:], "notes.md"); n == 0 {
		t.Fatalf("the return to the refused content was not judged (delta %v) — content refused, "+
			"fixed, and then restored must be judged in its own right", after[seen:])
	}
	if !refusedPath(t, e, proj, sess, "notes.md") {
		t.Fatalf("content that was refused, fixed, and then restored was not refused again — the " +
			"pass earned by the benign content in between was read as the FILE's verdict rather " +
			"than as that content's")
	}
}

// T014_03: content that has PASSED and not changed is exempt — the check is not
// re-asked.
//
// The positive half retention rests on, and the boundary "for as long as the
// content stays as it is" names: once a file passes, it must fall out of the
// re-fire until its content changes. Without this, T014_01 and T014_02 would both
// be satisfied by an engine that re-judges everything forever, which is a
// different bug that hides this one.
//
// A file passes in cycle 1; cycles 2 and 3 do unrelated work. The passed file must
// NOT be re-judged — its settled content already has a pass on record, so
// revalidation lets it be skipped. The unrelated files are the control: they ARE
// judged, so the silence about the passed file is a real skip rather than a check
// that stopped firing.
func TestT014_03_PassedUnchangedContentIsNotReJudged(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "judge", forbidSecretGuard, map[string]string{"judge.sh": judgeScript})

	const sess = "s-014-03"

	seen := 0
	e.Run(proj, sess, "write fine content", Turns("done", Write("w1", "settled.md", "benign")))
	first := e.FileGuardLedgerLines(proj, "judge", "log")
	if askedAbout(first[seen:], "settled.md") == 0 {
		t.Fatalf("the file was never judged in the first cycle: %v — there is no pass for the "+
			"skip to be about", first)
	}
	seen = len(first)

	for cycle := 2; cycle <= 3; cycle++ {
		moving := fmt.Sprintf("moving%d.md", cycle)
		e.Run(proj, sess, "unrelated work", Turns("done",
			Write(fmt.Sprintf("u%d", cycle), moving, "cycle content"),
		))
		lines := e.FileGuardLedgerLines(proj, "judge", "log")
		delta := lines[seen:]
		// The control: this cycle judged the file it actually changed.
		if !strings.Contains(strings.Join(delta, "\n"), "path=["+moving+"]") {
			t.Fatalf("cycle %d judged nothing it changed (delta %v) — the silence about the passed "+
				"file below would then prove nothing", cycle, delta)
		}
		if n := askedAbout(delta, "settled.md"); n > 0 {
			t.Fatalf("cycle %d re-judged a file that had already passed at unchanged content (%d "+
				"times, delta %v) — a passing verdict must end the re-firing, or every file ever "+
				"judged accumulates forever", cycle, n, delta)
		}
		seen = len(lines)
	}
}

// T014_04: a refusal survives alongside a pass for a DIFFERENT file.
//
// The narrow failure this catches: a store keeping one verdict per session, or per
// guardrail, rather than per file. Such a store would let the passing file
// overwrite the refused one's row, and the violation would go quiet next cycle.
//
// Cycle 1: one file refused, one file passing. Cycle 2: unrelated work. The
// refused file must still re-fire; the passing file must not.
func TestT014_04_ARefusalSurvivesAPassForAnotherFile(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "judge", forbidSecretGuard, map[string]string{"judge.sh": judgeScript})

	const sess = "s-014-04"

	seen := 0
	e.Run(proj, sess, "one bad, one good", Turns("done",
		Write("w1", "bad.md", "SECRET=hunter2"),
		Write("w2", "good.md", "benign"),
	))
	first := e.FileGuardLedgerLines(proj, "judge", "log")
	if askedAbout(first[seen:], "bad.md") == 0 || askedAbout(first[seen:], "good.md") == 0 {
		t.Fatalf("both files must be judged in the first cycle: %v", first)
	}
	seen = len(first)
	if !refusedPath(t, e, proj, sess, "bad.md") {
		t.Fatalf("the bad file was not refused, so there is no refusal to survive the other's pass")
	}

	e.Run(proj, sess, "unrelated work", Turns("done", Write("u", "unrelated.md", "fine")))
	after := e.FileGuardLedgerLines(proj, "judge", "log")
	delta := after[seen:]
	if n := askedAbout(delta, "bad.md"); n == 0 {
		t.Fatalf("the refusal on bad.md did not survive the pass recorded for good.md (delta %v) — "+
			"a pass recorded for one file must not settle another", delta)
	}
	if n := askedAbout(delta, "good.md"); n > 0 {
		t.Fatalf("the passed good.md was re-judged (%d times, delta %v) — its own pass should hold, "+
			"and if it did not the re-fire of bad.md would say nothing about per-file verdicts",
			n, delta)
	}
}
