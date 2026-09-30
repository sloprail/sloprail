package e2e

import (
	"encoding/json"
	"testing"
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

// recordEverything is a NEW-FORMAT file-guard that records every after-the-fact
// file event it is handed and permits unconditionally. `match: "**/*.md"` selects
// every markdown file at any depth — the faithful stand-in for the old binding to
// all three after-the-fact kinds, which a single file-guard now covers because it
// fires on whichever Post kind the change produced. The ledger (`seen`, no `.md`)
// is not matched, so the guard cannot re-observe its own bookkeeping. Re-vehicled
// from the old GUARDRAIL.md hooks per tests/e2e/REVEHICLE-PATTERN.md so this
// coverage of the shared difference machinery survives the old dispatch's
// deletion, observed through the new flat CheckPayload. An untouched file produces
// no Post event from the engine's diff, so the guard is never handed it — the
// same silence the old kind-bound hooks observed.
const recordEverything = `match: "**/*.md"
checks:
  - script: ./record.sh
`

const recordScript = `#!/bin/sh
cat >> "$SR_GUARDRAIL_DIR/seen"
echo >> "$SR_GUARDRAIL_DIR/seen"
exit 0
`

type observed struct {
	Kind string
	Path string
}

// observedFiles decodes what a file-guard's check was handed — the FLAT event,
// whose fields spread directly under `event` (`.event.kind`, `.event.path`), not
// the old nested `event.fields` envelope.
func observedFiles(t *testing.T, lines []string) []observed {
	t.Helper()
	var got []observed
	for _, line := range lines {
		var p struct {
			Event struct {
				Kind string `json:"kind"`
				Path string `json:"path"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("the check was handed something that is not an event payload: %v\n%s", err, line)
		}
		got = append(got, observed{Kind: p.Event.Kind, Path: p.Event.Path})
	}
	return got
}

func sawPath(got []observed, path string) bool {
	for _, o := range got {
		if o.Path == path {
			return true
		}
	}
	return false
}

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

// T017_01: the control — a file the cycle DID touch is reported.
//
// This must come first and must be read as part of every test below it. The
// project here is seeded with untouched files exactly as in T017_02, and the
// session touches one of them. If this fails, the ledger in this directory
// cannot register anything at all, and every absence asserted below is vacuous.
func TestT017_01_ATouchedFileIsReported(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": recordScript})
	writeFile(t, proj, "old-one.md", "original\n")
	writeFile(t, proj, "old-two.md", "original\n")
	seedUntouched(e, proj)

	e.Run(proj, "s-017-01", "touch one file", Turns("done",
		Write("w1", "old-one.md", "changed by the agent\n"),
	))

	got := observedFiles(t, e.FileGuardLedgerLines(proj, "watcher", "seen"))
	if !sawPath(got, "old-one.md") {
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
func TestT017_02_AnUntouchedFileProducesNothing(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": recordScript})
	writeFile(t, proj, "touched.md", "original\n")
	writeFile(t, proj, "untouched.md", "original\n")
	seedUntouched(e, proj)

	e.Run(proj, "s-017-02", "touch one of two", Turns("done",
		Write("w1", "touched.md", "changed by the agent\n"),
	))

	got := observedFiles(t, e.FileGuardLedgerLines(proj, "watcher", "seen"))
	// The positive half, in this same session. Without it the next assertion
	// holds for an engine that dispatched nothing whatsoever.
	if !sawPath(got, "touched.md") {
		t.Fatalf("the changed file is missing from %v — nothing was observed, so the "+
			"silence about the untouched file below proves nothing", got)
	}
	if sawPath(got, "untouched.md") {
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
func TestT017_03_AFileRestoredToItsOriginalIsNotReported(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": recordScript})
	writeFile(t, proj, "round-trip.md", "original\n")
	writeFile(t, proj, "genuinely-changed.md", "original\n")
	seedUntouched(e, proj)

	e.Run(proj, "s-017-03", "change one back", Turns("done",
		Write("w1", "round-trip.md", "temporarily different\n"),
		Write("w2", "genuinely-changed.md", "left different\n"),
		// Restored to the exact bytes the baseline holds.
		Write("w3", "round-trip.md", "original\n"),
	))

	got := observedFiles(t, e.FileGuardLedgerLines(proj, "watcher", "seen"))
	if !sawPath(got, "genuinely-changed.md") {
		t.Fatalf("the file left different is missing from %v — nothing was observed, so the "+
			"silence about the restored file proves nothing", got)
	}
	if sawPath(got, "round-trip.md") {
		t.Fatalf("a file restored to its baseline content was reported as changed: %v — "+
			"the difference is against the tree at the baseline, not a log of what was written", got)
	}
}
