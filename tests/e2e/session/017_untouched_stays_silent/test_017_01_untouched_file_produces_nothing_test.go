package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
	"github.com/sloprail/sloprail/tests/e2e/session/changesetkit"
)

// untouched_stays_silent: a file unchanged since the point the difference is
// measured from produces no event.
//
// The spec's reasoning: "Everything the project has ever contained is not this
// session's work. Reporting it would put the entire repository in front of every
// guardrail on the first cycle, and bury whatever the agent actually did."
//
// This is the invariant most easily faked. "Nothing happens" is the easiest
// assertion in software to write so that it can never fail: an empty ledger
// satisfies it whether the engine correctly stayed silent, dispatched to a
// binding that does not exist, crashed before dispatching, or was never asked.
// Every one of those reads as a pass.
//
// So no test in this directory asserts an absence without first proving, in the
// SAME session and through the SAME ledger, that a change WOULD have been
// recorded. T017_01 is that control and it is not optional scaffolding — it is
// what converts the silence below it from "nothing arrived" into "nothing
// arrived about this file, while something arrived about that one".

// recordEverything is a NEW-FORMAT file-guard that records every Changeset it is handed and permits unconditionally. `match: "**/*.md"` selects
// every markdown file at any depth — a single file-guard that fires on every committed change. The ledger (`seen`, no `.md`)
// is not matched, so the guard cannot re-observe its own bookkeeping. An untouched file is not in the
// changeset, so the guard is never handed it.
const recordEverything = `match: "**/*.md"
checks:
  - script: ./record.sh
`

// seedUntouched writes the files that exist BEFORE the session starts and
// commits them, so they are part of the baseline rather than part of the
// cycle's work.
//
// Committed deliberately. A file merely present but uncommitted at session
// start is outstanding work in the tree, and whether it belongs to the cycle is
// a different question (016's). Here the files must be unambiguously old.
func seedUntouched(e *Env, proj string) {
	e.CommitAll(proj, "pre-existing project files")
}

// installWatcher adds the rule in its OWN commit, after the seed: a file-guard's
// range starts at the parent of the commit that last touched its folder, so a
// seed committed together with the rule would fall inside the first range.
func installWatcher(e *Env, proj string, led *harness.Ledger) {
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": led.RecordScript()})
	e.CommitAll(proj, "install the rule")
}

// T017_01: the control — a file the cycle DID touch is reported.
//
// This must come first and must be read as part of every test below it. The
// project here is seeded with untouched files exactly as in T017_02, and the
// session touches one of them. If this fails, the ledger in this directory
// cannot register anything at all, and every absence asserted below is vacuous.
// sr:proves events/post-changes-are-the-tree-diff
func TestT017_01_ATouchedFileIsReported(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	writeFile(t, proj, "old-one.md", "original\n")
	writeFile(t, proj, "old-two.md", "original\n")
	seedUntouched(e, proj)
	led := e.NewLedger("seen")
	installWatcher(e, proj, led)

	e.Run(proj, "s-017-01", "touch one file", Turns("done",
		Write("w1", "old-one.md", "changed by the agent\n"),
	).ThenCommit("the work"))

	got := changesetkit.Files(t, led.Lines())
	if !changesetkit.Saw(got, "old-one.md") {
		t.Fatalf("the file the cycle changed was not reported: got %v — "+
			"this ledger cannot register a change, so no absence asserted in this directory means anything", got)
	}
}

// T017_02: the files the cycle did not touch produce nothing.
//
// The invariant proper. Same project shape as the control above: two committed
// files, one of them changed. The changed one must appear — asserted here again
// rather than assumed from T017_01, because these are separate sessions and a
// build could dispatch in one and not the other — and the untouched one must
// not.
//
// The untouched file is not merely absent from a list that might be empty: the
// assertion below runs against a ledger that has just been shown to contain the
// other file.
// sr:proves events/post-changes-are-the-tree-diff
func TestT017_02_AnUntouchedFileProducesNothing(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	writeFile(t, proj, "touched.md", "original\n")
	writeFile(t, proj, "untouched.md", "original\n")
	seedUntouched(e, proj)
	led := e.NewLedger("seen")
	installWatcher(e, proj, led)

	e.Run(proj, "s-017-02", "touch one of two", Turns("done",
		Write("w1", "touched.md", "changed by the agent\n"),
	).ThenCommit("the work"))

	got := changesetkit.Files(t, led.Lines())
	// The positive half, in this same session. Without it the next assertion
	// holds for an engine that dispatched nothing whatsoever.
	if !changesetkit.Saw(got, "touched.md") {
		t.Fatalf("the changed file is missing from %v — nothing was observed, so the "+
			"silence about the untouched file below proves nothing", got)
	}
	if changesetkit.Saw(got, "untouched.md") {
		t.Fatalf("a file unchanged since the session began was reported as this cycle's work: %v — "+
			"on a real project this puts the whole repository in front of every guardrail", got)
	}
}

// T017_03: a file changed and then put back is not reported.
//
// The boundary case of "unchanged since the point the difference is measured
// from". The file is modified mid-cycle and restored to its original bytes
// before the cycle ends. It was touched, but at the moment the difference is
// taken it is identical to the baseline — so by the predicate it must be
// silent.
//
// This separates an engine that compares the tree against the baseline from one
// that accumulates every path it noticed being written during the cycle. Both
// pass T017_02; only the comparing one passes this.
// sr:proves events/post-changes-are-the-tree-diff
func TestT017_03_AFileRestoredToItsOriginalIsNotReported(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	writeFile(t, proj, "round-trip.md", "original\n")
	writeFile(t, proj, "genuinely-changed.md", "original\n")
	seedUntouched(e, proj)
	led := e.NewLedger("seen")
	installWatcher(e, proj, led)

	e.Run(proj, "s-017-03", "change one back", Turns("done",
		Write("w1", "round-trip.md", "temporarily different\n"),
		Write("w2", "genuinely-changed.md", "left different\n"),
		// Restored to the exact bytes the baseline holds.
		Write("w3", "round-trip.md", "original\n"),
	).ThenCommit("the work"))

	got := changesetkit.Files(t, led.Lines())
	if !changesetkit.Saw(got, "genuinely-changed.md") {
		t.Fatalf("the file left different is missing from %v — nothing was observed, so the "+
			"silence about the restored file proves nothing", got)
	}
	if changesetkit.Saw(got, "round-trip.md") {
		t.Fatalf("a file restored to its baseline content was reported as changed: %v — "+
			"the difference is against the tree at the baseline, not a log of what was written", got)
	}
}
