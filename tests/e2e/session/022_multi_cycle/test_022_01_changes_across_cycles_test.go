package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
	"github.com/sloprail/sloprail/tests/e2e/session/changesetkit"
)

// What a file's history across CYCLES looks like to the rules bound to it.
//
// Almost every existing session test is one cycle long, and the interesting
// answers are not.
//
// THE MODEL, stated once here because getting it wrong is what makes these
// tests assert the opposite of the truth. A file-guard judges COMMITS: at each
// Stop it is handed the net change of the range of commits it has not yet
// passed, from its watermark (the head of its last passing run) to HEAD. So the
// measuring point MOVES with every pass: a file one cycle added is already in the
// base of the next cycle's range, and editing it there is a modification (M), not
// another addition. Within one range, several commits squash into one net change:
// a file added and removed, or removed and restored to the bytes it had, is not in
// the changeset at all.
//
// The agent commits its work before it stops (ThenCommit); uncommitted guarded
// work is refused at Stop instead of judged.
//
// Each test drives several Run calls under ONE session id, which is what makes
// them cycles of one session rather than unrelated sessions. The ledger
// accumulates across them, so every assertion reads the DELTA a cycle added
// rather than a total — a total cannot tell "cycle two reported it" from "cycle
// one reported it twice", and that is exactly the confusion this file exists to
// resolve.

// recordEverything is a file-guard that records every changeset it is handed
// (`.changeset.files[]`, each {path, status A/M/D}) and passes. `match: "**/*.md"`
// selects markdown at any depth; `deletions: include` because this guard observes
// EVERY change and a file-guard skips deleted files unless it says so. The ledger
// (`seen`, no `.md`) is not matched, so the guard cannot re-observe its own
// bookkeeping.
const recordEverything = `match: "**/*.md"
deletions: include
checks:
  - script: ./record.sh
`

// cycles drives a sequence of cycles under one session id and returns, for each,
// only the events THAT cycle added to the ledger.
//
// The per-cycle slicing is the whole helper. Reading totals is how a multi-cycle
// test silently stops testing anything: eight sightings after cycle one and
// eight after cycle two is indistinguishable from a second cycle that reported
// nothing, and a "reported again" assertion written against a total passes on
// input where nothing was reported again at all.
func cycles(t *testing.T, e *harness.Env, proj, ledger, sess string, scenarios ...harness.Scenario) [][]changesetkit.Observed {
	t.Helper()
	var out [][]changesetkit.Observed
	seen := 0
	for i, s := range scenarios {
		e.Run(proj, sess, "cycle", s)
		lines := harness.ReadLedgerLines(t, ledger)
		if len(lines) < seen {
			t.Fatalf("cycle %d: the ledger shrank (%d lines, was %d)", i+1, len(lines), seen)
		}
		out = append(out, changesetkit.Files(t, lines[seen:]))
		seen = len(lines)
	}
	return out
}

// project is a repository with the recording guardrail already committed, so the
// rule's own folder is part of the history rather than an uncommitted guarded
// change of every cycle. It returns the path of the ledger the check records to.
//
// seed is content that exists before the rule does, committed in its own commit
// BEFORE the rule's: a rule's range floors at the parent of the last commit
// touching its own folder, so the seed must sit at or below that parent to be
// part of the base rather than of the first range.
func project(t *testing.T, seed ...[2]string) (*harness.Env, string, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	ledger := filepath.Join(t.TempDir(), "seen")
	e.GitInit(proj)
	for _, f := range seed {
		e.WriteFile(proj, f[0], f[1])
	}
	if len(seed) > 0 {
		e.CommitAll(proj, "the project before the rule")
	}
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": harness.RecordScript(ledger)})
	e.DisableShippedFileGuards(proj)
	e.CommitAll(proj, "the rule, before the session")
	return e, proj, ledger
}

// T022_01: a file added in one cycle and edited in the next is a modification in
// the second.
//
// The measuring point is the rule's watermark, and a passing cycle moves it. Cycle
// one's range holds the file as an addition; once that range passed, the file is
// part of the base of cycle two's, so the edit is an M of a file the range's base
// holds. (On the old session-baseline model it stayed a create forever.) The
// discriminator is the range's base, not "have we mentioned it before".
func TestT022_01_AFileAddedInOneCycleIsModifiedInTheNext(t *testing.T) {
	e, proj, ledger := project(t)

	got := cycles(t, e, proj, ledger, "s-022-01",
		Turns("done", Write("w1", "subject.md", "first version\n")).ThenCommit("add subject"),
		Turns("done", Write("w2", "subject.md", "second version\n")).ThenCommit("edit subject"),
	)
	first, second := got[0], got[1]

	if k := changesetkit.Statuses(first, "subject.md"); len(k) != 1 || k[0] != "A" {
		t.Fatalf("cycle one did not report the new file as an addition: %v (all: %v)", k, first)
	}
	k := changesetkit.Statuses(second, "subject.md")
	if len(k) != 1 {
		t.Fatalf("cycle two did not report the file it edited exactly once: %v — the cycle "+
			"dispatched nothing, or its range still reached back before the pass", second)
	}
	if k[0] != "M" {
		t.Fatalf("cycle two reported %v for a file the passed range already added; want M — "+
			"the watermark did not advance past cycle one", k)
	}
}

// T022_01b: a file that was in the history before the session is a modification
// whenever this session edits it, in the first cycle and in a later one alike.
//
// The other side of T022_01, and what stops it being read as "everything is an
// addition". Without this pair, an engine that classified everything as an A would
// pass T022_01's first half perfectly.
func TestT022_01b_AFileInTheHistoryIsModifiedInEveryCycle(t *testing.T) {
	e, proj, ledger := project(t, [2]string{"pre-existing.md", "original\n"})

	got := cycles(t, e, proj, ledger, "s-022-01b",
		Turns("done", Write("w1", "pre-existing.md", "edited once\n")).ThenCommit("edit once"),
		Turns("done", Write("w2", "pre-existing.md", "edited twice\n")).ThenCommit("edit twice"),
	)

	for i, this := range got {
		k := changesetkit.Statuses(this, "pre-existing.md")
		if len(k) == 0 {
			t.Fatalf("cycle %d never reported the file it edited: %v", i+1, this)
		}
		if k[0] != "M" {
			t.Fatalf("cycle %d reported %v for a file already in the history; want M", i+1, k)
		}
	}
}

// T022_02: a file added and deleted within ONE range is reported as neither.
//
// Two commits in one cycle — one adds the file, the next removes it — squash to
// nothing: the range's net change has no such path. This is the boundary of
// "observed rather than announced": a rule asked about the path would be judging a
// file that is not there.
//
// The control is a second file the same cycle leaves in place, so "nothing was
// reported" cannot pass on a cycle that dispatched nothing at all.
func TestT022_02_AddedAndDeletedInTheSameRangeIsSilent(t *testing.T) {
	e, proj, ledger := project(t)

	got := cycles(t, e, proj, ledger, "s-022-02",
		Turns("done",
			Write("w1", "ephemeral.md", "here and gone\n"),
			harness.Commit("c1", "add ephemeral"),
			Bash("b1", "rm ephemeral.md"),
			Write("w2", "survivor.md", "still here\n"),
		).ThenCommit("remove ephemeral, add survivor"),
	)
	only := got[0]

	if !changesetkit.Saw(only, "survivor.md") {
		t.Fatalf("the file the cycle left behind was not reported: %v — nothing was observed, "+
			"so the silence about the ephemeral file proves nothing", only)
	}
	if k := changesetkit.Statuses(only, "ephemeral.md"); len(k) > 0 {
		t.Fatalf("a file added and removed within the range was reported as %v: %v\n"+
			"the range's net change does not hold it — a rule asked about this path would be "+
			"judging a file that is not there", k, only)
	}
}

// T022_03: a file deleted in one cycle and recreated in the next is a D, then an A.
//
// Each cycle's range starts where the last passing one ended. Cycle one removes the
// file: it was in that range's base and is gone at its head, a delete. Cycle two's
// base is the tree without it, so putting it back is an addition, whatever bytes
// it holds. An engine that kept measuring from the session's start would report
// an M here; one that cancelled the delete against the recreate would report
// nothing and the recreated content would go unjudged.
func TestT022_03_DeletedThenRecreatedIsADeleteThenAnAddition(t *testing.T) {
	e, proj, ledger := project(t, [2]string{"revenant.md", "original\n"})

	got := cycles(t, e, proj, ledger, "s-022-03",
		Turns("done", Bash("b1", "rm revenant.md")).ThenCommit("delete revenant"),
		Turns("done", Write("w1", "revenant.md", "back again, and different\n")).ThenCommit("recreate revenant"),
	)
	first, second := got[0], got[1]

	if k := changesetkit.Statuses(first, "revenant.md"); len(k) != 1 || k[0] != "D" {
		t.Fatalf("cycle one did not report the removal as a delete: %v (all: %v)", k, first)
	}
	k := changesetkit.Statuses(second, "revenant.md")
	if len(k) != 1 {
		t.Fatalf("cycle two never reported the recreated file: %v — content the passed range "+
			"did not hold went unjudged entirely", second)
	}
	if k[0] != "A" {
		t.Fatalf("cycle two reported %v for a path the passed range's head lacks; want A", k)
	}
}

// T022_03b: a file removed and restored to EXACTLY its bytes within one range falls
// silent.
//
// The boundary of the case above, and what separates comparing from
// accumulating. Two commits in one cycle — delete, then recreate with the bytes
// the range's base holds — net to nothing, so by untouched_stays_silent the range
// has nothing to report about it. An engine remembering that the path was deleted
// mid-range would report a D or an M for a file exactly as it was found. The
// control is a second file the same cycle genuinely changes.
func TestT022_03b_RestoredWithTheBasesBytesWithinARangeIsSilent(t *testing.T) {
	const original = "original\n"
	e, proj, ledger := project(t, [2]string{"round-trip.md", original})

	got := cycles(t, e, proj, ledger, "s-022-03b",
		Turns("done",
			Bash("b1", "rm round-trip.md"),
			harness.Commit("c1", "delete round-trip"),
			Write("w1", "round-trip.md", original),
			Write("w2", "genuinely-changed.md", "new work\n"),
		).ThenCommit("restore round-trip, add new work"),
	)
	second := got[0]

	if !changesetkit.Saw(second, "genuinely-changed.md") {
		t.Fatalf("the cycle reported nothing at all: %v — the silence about the restored file "+
			"below would prove nothing", second)
	}
	if k := changesetkit.Statuses(second, "round-trip.md"); len(k) > 0 {
		t.Fatalf("a file restored to its base bytes was reported as %v: %v\n"+
			"the changeset is the net difference between two trees, not a log of what was "+
			"done to the path along the way", k, second)
	}
}

// T022_04: a file left alone after a rule passed it is not put in front of that
// rule again.
//
// The passing run moved the rule's watermark to its head, so the next range
// starts after everything that was judged: the untouched file is in the base, not
// in the change. What it catches is an engine whose range keeps reaching back to
// the session's start and re-judges everything it can still see.
//
// The positive is asserted in the same cycle and the same ledger slice that the
// negative is read from.
func TestT022_04_AFileLeftAloneAfterBeingReportedFallsSilent(t *testing.T) {
	e, proj, ledger := project(t)

	got := cycles(t, e, proj, ledger, "s-022-04",
		Turns("done",
			Write("w1", "kept.md", "one\n"),
			Write("w2", "moved-on.md", "one\n"),
		).ThenCommit("add both"),
		Turns("done", Write("w3", "kept.md", "two\n")).ThenCommit("edit kept"),
	)
	first, second := got[0], got[1]

	if !changesetkit.Saw(first, "kept.md") || !changesetkit.Saw(first, "moved-on.md") {
		t.Fatalf("cycle one did not report both files: %v", first)
	}
	if !changesetkit.Saw(second, "kept.md") {
		t.Fatalf("cycle two did not report the file it edited: %v — nothing was observed, so "+
			"the silence about the other file proves nothing", second)
	}
	if k := changesetkit.Statuses(second, "moved-on.md"); len(k) > 0 {
		t.Fatalf("a file untouched since the previous cycle was reported again as %v: %v\n"+
			"the range starts at the last pass; reporting it again puts settled work in front "+
			"of every rule forever", k, second)
	}
}

// T022_05: five cycles in one session, each putting only its own work in front
// of the rule.
//
// The compounding case. Every cycle writes and commits a file named for itself;
// what keeps the rule from being re-asked about the earlier ones is that its
// watermark moved past them when they passed. An engine whose range did not
// advance hands the rule a growing pile: by cycle five, five files where one
// belongs, each re-ask a fresh model call free to come back with a different
// answer about work the agent has moved on from.
//
// Each cycle is checked as it goes rather than only at the end, so a failure
// names the cycle the advance stopped holding on.
func TestT022_05_FiveCyclesEachReportOnlyTheirOwnWork(t *testing.T) {
	e, proj, ledger := project(t)

	names := []string{"c1.md", "c2.md", "c3.md", "c4.md", "c5.md"}
	var scenarios []harness.Scenario
	for i, n := range names {
		scenarios = append(scenarios, Turns("done",
			Write(strings.Repeat("w", i+1), n, "written in cycle\n")).ThenCommit("cycle "+n))
	}

	got := cycles(t, e, proj, ledger, "s-022-05", scenarios...)

	for i, own := range names {
		this := got[i]
		if !changesetkit.Saw(this, own) {
			t.Fatalf("cycle %d did not report its own file %q: %v — this cycle observed "+
				"nothing, so what it left out proves nothing", i+1, own, this)
		}
		for _, earlier := range names[:i] {
			if k := changesetkit.Statuses(this, earlier); len(k) > 0 {
				t.Fatalf("cycle %d was handed %q again as %v: %v\nthe watermark stopped "+
					"advancing, so every later cycle re-reports the whole session's work",
					i+1, earlier, k, this)
			}
		}
	}
}
