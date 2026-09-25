package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// What a file's history across CYCLES looks like to the rules bound to it.
//
// Almost every existing session test is one cycle long, and the interesting
// answers are not.
//
// THE MODEL, stated once here because getting it wrong is what makes these
// tests assert the opposite of the truth. The measuring point is where the
// SESSION began and it does NOT move at the end of each cycle: ensureBaseline
// re-takes it only when the tree leaves the history it sits in, precisely so
// that an agent committing mid-session cannot push its own work out of the
// difference. So a file this session created is absent from the baseline for
// the whole session, and every cycle that touches it reports a CREATE.
//
// Two separate mechanisms then decide what a rule actually sees, and this file
// keeps them apart:
//
//   - the DIFFERENCE, from gitrepo.Changed, which answers "does this path
//     differ from the session's baseline" and keeps answering yes;
//   - the REVALIDATION record, which exempts content a guardrail has already
//     judged and passed, and is what makes a quiet cycle quiet.
//
// Conflating them is easy and produces tests that pass for the wrong reason, so
// each test below names which one it is about.
//
// Each test drives several Run calls under ONE session id, which is what makes
// them cycles of one session rather than unrelated sessions. The ledger
// accumulates across them, so every assertion reads the DELTA a cycle added
// rather than a total — a total cannot tell "cycle two reported it" from "cycle
// one reported it twice", and that is exactly the confusion this file exists to
// resolve.

// recordEverything is a NEW-FORMAT file-guard that records every after-the-fact
// file event it is handed (re-vehicled from the old GUARDRAIL.md hooks per
// tests/e2e/REVEHICLE-PATTERN.md). `match: "**/*.md"` fires on whichever Post kind
// each change produced — create, update or delete (it sets `deletions: include`) — so the kind assertions below
// read the SAME classification through the new dispatch. The quiet-cycle skip
// (T022_04/05) is the file-guard's own revalidation record, the same mechanism the
// old dispatch used. The ledger (`seen`, no `.md`) is not matched, so the guard
// cannot re-observe its own bookkeeping.
const recordEverything = `match: "**/*.md"
# deletions: include — this guard observes EVERY change, and a file-guard
# skips deleted files unless it says so.
deletions: include
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

// kindsFor is every kind reported for one path, in arrival order.
func kindsFor(got []observed, path string) []string {
	var out []string
	for _, o := range got {
		if o.Path == path {
			out = append(out, o.Kind)
		}
	}
	return out
}

func sawPath(got []observed, path string) bool { return len(kindsFor(got, path)) > 0 }

// cycles drives a sequence of cycles under one session id and returns, for each,
// only the events THAT cycle added to the ledger.
//
// The per-cycle slicing is the whole helper. Reading totals is how a multi-cycle
// test silently stops testing anything: eight sightings after cycle one and
// eight after cycle two is indistinguishable from a second cycle that reported
// nothing, and a "reported again" assertion written against a total passes on
// input where nothing was reported again at all.
func cycles(t *testing.T, e *harness.Env, proj, sess string, scenarios ...harness.Scenario) [][]observed {
	t.Helper()
	var out [][]observed
	seen := 0
	for i, s := range scenarios {
		e.Run(proj, sess, "cycle", s)
		lines := e.FileGuardLedgerLines(proj, "watcher", "seen")
		if len(lines) < seen {
			t.Fatalf("cycle %d: the ledger shrank (%d lines, was %d)", i+1, len(lines), seen)
		}
		out = append(out, observedFiles(t, lines[seen:]))
		seen = len(lines)
	}
	return out
}

// project is a repository with the recording guardrail already committed, so the
// rule's own folder is part of the baseline rather than part of every cycle's
// difference.
func project(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": recordScript})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")
	return e, proj
}

// T022_01: a file created in one cycle and edited in the next is still a create
// against the SESSION's baseline.
//
// The measuring point is where the SESSION began, not where the last cycle
// ended: ensureBaseline moves it only when the tree leaves the history it sits
// in, and an agent committing mid-session explicitly must not push its own work
// out of the difference. So a file this session created is absent from the
// baseline for as long as that point stands, and every cycle reports it as a
// create however many times the session goes on to edit it.
//
// That is the opposite of the intuition "the second write is an update", and it
// is worth a test precisely because the intuition is wrong in a way nothing else
// here would catch. What the classification means is "was this path at the point
// the difference is measured from" — not "have we mentioned it before".
//
// The consequence is real for rule authors and is asserted rather than described:
// a rule bound to PostFileCreate sees this file on every cycle that touches it,
// and one bound to PostFileUpdate never sees it at all.
func TestT022_01_AFileThisSessionCreatedStaysACreateAcrossCycles(t *testing.T) {
	e, proj := project(t)

	got := cycles(t, e, proj, "s-022-01",
		Turns("done", Write("w1", "subject.md", "first version\n")),
		Turns("done", Write("w2", "subject.md", "second version\n")),
	)
	first, second := got[0], got[1]

	if k := kindsFor(first, "subject.md"); len(k) == 0 || k[0] != "PostFileCreate" {
		t.Fatalf("cycle one did not report the new file as a create: %v (all: %v)", k, first)
	}
	k := kindsFor(second, "subject.md")
	if len(k) == 0 {
		t.Fatalf("cycle two never reported the file it edited: %v — the session's baseline "+
			"moved out from under a file the session itself created, or the cycle dispatched "+
			"nothing", second)
	}
	if k[0] != "PostFileCreate" {
		t.Fatalf("cycle two reported %v for a file absent from the session's baseline; want "+
			"PostFileCreate — the measuring point is where the SESSION began, so a file it "+
			"created is not at that point however many cycles have edited it since", k)
	}
}

// T022_01b: a file that WAS at the session's baseline is an update when this
// session edits it, in the first cycle and in a later one alike.
//
// The other side of T022_01, and what stops it being read as "everything is a
// create". The discriminator is the baseline, not the cycle count: this file is
// committed before the session starts, so it is at the measuring point, and
// every cycle that changes it reports an update.
//
// Without this pair, an engine that classified everything as a create would pass
// T022_01 perfectly.
func TestT022_01b_AFileAtTheBaselineIsAnUpdateInEveryCycle(t *testing.T) {
	e, proj := project(t)
	e.WriteFile(proj, "pre-existing.md", "original\n")
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "a file the session will edit")

	got := cycles(t, e, proj, "s-022-01b",
		Turns("done", Write("w1", "pre-existing.md", "edited once\n")),
		Turns("done", Write("w2", "pre-existing.md", "edited twice\n")),
	)

	for i, this := range got {
		k := kindsFor(this, "pre-existing.md")
		if len(k) == 0 {
			t.Fatalf("cycle %d never reported the file it edited: %v", i+1, this)
		}
		if k[0] != "PostFileUpdate" {
			t.Fatalf("cycle %d reported %v for a file present at the session's baseline; want "+
				"PostFileUpdate — classifying it as a create would send every pre-existing "+
				"file to the rules that judge new ones", i+1, k)
		}
	}
}

// T022_02: a file created and deleted within ONE cycle is reported as neither.
//
// The tree at the end of the cycle is exactly what the baseline had, so by
// untouched_stays_silent there is nothing to report: no content to judge, and
// no absence that differs from the baseline. This is the boundary of "observed
// rather than announced" — an engine accumulating the paths it saw written
// would report a create for a file that is not there, and a rule would be asked
// to judge content that does not exist.
//
// The control is a second file the same cycle leaves in place, so "nothing was
// reported" cannot pass on a cycle that dispatched nothing at all.
func TestT022_02_CreatedAndDeletedInTheSameCycleIsSilent(t *testing.T) {
	e, proj := project(t)

	got := cycles(t, e, proj, "s-022-02",
		Turns("done",
			Write("w1", "ephemeral.md", "here and gone\n"),
			Write("w2", "survivor.md", "still here\n"),
			Bash("b1", "rm ephemeral.md"),
		),
	)
	only := got[0]

	// The positive first: this cycle really did report something.
	if !sawPath(only, "survivor.md") {
		t.Fatalf("the file the cycle left behind was not reported: %v — nothing was observed, "+
			"so the silence about the ephemeral file proves nothing", only)
	}
	if k := kindsFor(only, "ephemeral.md"); len(k) > 0 {
		t.Fatalf("a file created and removed within the cycle was reported as %v: %v\n"+
			"the tree ends the cycle as the baseline had it, so there is no content to judge — "+
			"a rule asked about this path would be judging a file that is not there", k, only)
	}
}

// T022_03: a file deleted in one cycle and recreated in the next is a delete
// then an update, both measured against the session's baseline.
//
// The sequence a naive engine gets wrong in two different ways. The file is at
// the session's baseline, so:
//
//   - cycle one removes it: it was at the point and is gone now, which is a
//     delete;
//   - cycle two puts it back with DIFFERENT content: it was at the point and is
//     there now holding other bytes, which is an update — not a create, because
//     the baseline still holds the path, and not silence, because the content
//     differs from what the baseline has.
//
// An engine tracking "what happened during this session" rather than comparing
// against the point reports a create for cycle two, and the rules that judge new
// files run on a file that is not new. One that cancelled the delete against the
// recreate would report nothing at all and the changed content would go unjudged.
func TestT022_03_DeletedThenRecreatedIsADeleteThenAnUpdate(t *testing.T) {
	e, proj := project(t)

	// Committed before the session so the file is unambiguously part of the
	// baseline rather than this session's own work.
	e.WriteFile(proj, "revenant.md", "original\n")
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "a file the session will delete")

	got := cycles(t, e, proj, "s-022-03",
		Turns("done", Bash("b1", "rm revenant.md")),
		Turns("done", Write("w1", "revenant.md", "back again, and different\n")),
	)
	first, second := got[0], got[1]

	if k := kindsFor(first, "revenant.md"); len(k) == 0 || k[0] != "PostFileDelete" {
		t.Fatalf("cycle one did not report the removal as a delete: %v (all: %v)", k, first)
	}
	k := kindsFor(second, "revenant.md")
	if len(k) == 0 {
		t.Fatalf("cycle two never reported the recreated file: %v — content that differs from "+
			"the baseline went unjudged entirely", second)
	}
	if k[0] != "PostFileUpdate" {
		t.Fatalf("cycle two reported %v for a path its baseline still holds; want "+
			"PostFileUpdate — the file is not new at that path, and reporting a create sends "+
			"it to the rules that judge files arriving for the first time", k)
	}
}

// T022_03b: a file recreated with EXACTLY the baseline's bytes falls silent.
//
// The boundary of the case above, and the one that separates comparing from
// accumulating. Same sequence — delete, then recreate — except the content put
// back is byte-identical to what the baseline holds. At the moment the second
// cycle's difference is taken the tree matches the point, so by
// untouched_stays_silent there is nothing to report.
//
// An engine remembering that the path was deleted earlier in the session
// reports a delete or an update here, for a file that is exactly as the session
// found it. The control is a second file the same cycle genuinely changes.
func TestT022_03b_RecreatedWithTheBaselinesBytesIsSilent(t *testing.T) {
	e, proj := project(t)

	const original = "original\n"
	e.WriteFile(proj, "round-trip.md", original)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "a file the session will delete and restore")

	got := cycles(t, e, proj, "s-022-03b",
		Turns("done", Bash("b1", "rm round-trip.md")),
		Turns("done",
			Write("w1", "round-trip.md", original),
			Write("w2", "genuinely-changed.md", "new work\n"),
		),
	)
	first, second := got[0], got[1]

	// Cycle one really removed it, or the second cycle is not restoring anything.
	if k := kindsFor(first, "round-trip.md"); len(k) == 0 || k[0] != "PostFileDelete" {
		t.Fatalf("cycle one did not report the removal as a delete: %v (all: %v)", k, first)
	}
	// The positive, in the same slice the absence is read from.
	if !sawPath(second, "genuinely-changed.md") {
		t.Fatalf("cycle two reported nothing at all: %v — the silence about the restored file "+
			"below would prove nothing", second)
	}
	if k := kindsFor(second, "round-trip.md"); len(k) > 0 {
		t.Fatalf("a file restored to its baseline bytes was reported as %v: %v\n"+
			"the difference is against the tree at the baseline, not a log of what the session "+
			"did to the path along the way", k, second)
	}
}

// T022_04: a file left alone after a rule passed it is not put in front of that
// rule again.
//
// WHICH MECHANISM THIS IS, because there are two and the difference matters.
// The file does NOT leave the difference: the baseline is the session's start
// and the file is still absent from it, so gitrepo.Changed reports it every
// cycle. What stops it reaching the hook is REVALIDATION — the guardrail
// already judged those exact bytes and passed them, so rev.Skip exempts it.
// Verified rather than assumed: with the ledger instrumented, cycle two's
// difference still holds the untouched path and only the dispatch drops it.
//
// So this is the multi-cycle form of "a hook is not re-asked a settled
// question", and what it catches is an engine that re-judges everything it can
// still see — which by the twentieth cycle is the whole session, and which
// invites a different verdict on work the agent can no longer reach to fix.
//
// This is the shape the brief calls dangerous, so it is built the safe way
// round: the positive is asserted in the same cycle and the same ledger slice
// that the negative is read from.
func TestT022_04_AFileLeftAloneAfterBeingReportedFallsSilent(t *testing.T) {
	e, proj := project(t)

	got := cycles(t, e, proj, "s-022-04",
		Turns("done",
			Write("w1", "kept.md", "one\n"),
			Write("w2", "moved-on.md", "one\n"),
		),
		Turns("done", Write("w3", "kept.md", "two\n")),
	)
	first, second := got[0], got[1]

	// Cycle one really reported both, or "cycle two stayed silent about one of
	// them" is a statement about a file nothing ever saw.
	if !sawPath(first, "kept.md") || !sawPath(first, "moved-on.md") {
		t.Fatalf("cycle one did not report both files: %v", first)
	}
	// Cycle two reported the one it touched — the positive, in the same slice
	// the absence below is read from.
	if !sawPath(second, "kept.md") {
		t.Fatalf("cycle two did not report the file it edited: %v — nothing was observed, so "+
			"the silence about the other file proves nothing", second)
	}
	if k := kindsFor(second, "moved-on.md"); len(k) > 0 {
		t.Fatalf("a file untouched since the previous cycle was reported again as %v: %v\n"+
			"the difference is re-measured from the new baseline each cycle; reporting it again "+
			"puts settled work in front of every rule forever", k, second)
	}
}

// T022_05: five cycles in one session, each putting only its own work in front
// of the rule.
//
// The compounding case, and the reason the read mark and the revalidation
// record exist at all. Every cycle writes a file named for itself. All five
// remain in the DIFFERENCE throughout — the baseline is the session's start, so
// nothing this session wrote is ever at that point — and what keeps the rule
// from being re-asked about the earlier four is that it has already passed those
// exact bytes.
//
// An engine that did not remember its verdicts hands the rule a growing pile:
// by cycle five, five files where one belongs. That is the flood
// untouched_stays_silent exists to prevent, arriving one cycle at a time
// instead of all at once — and each re-ask is a fresh model call free to come
// back with a different answer about work the agent has moved on from.
//
// Each cycle is checked as it goes rather than only at the end, so a failure
// names the cycle the exemption stopped holding on.
func TestT022_05_FiveCyclesEachReportOnlyTheirOwnWork(t *testing.T) {
	e, proj := project(t)

	names := []string{"c1.md", "c2.md", "c3.md", "c4.md", "c5.md"}
	var scenarios []harness.Scenario
	for i, n := range names {
		scenarios = append(scenarios, Turns("done",
			Write(strings.Repeat("w", i+1), n, "written in cycle\n")))
	}

	got := cycles(t, e, proj, "s-022-05", scenarios...)

	for i, own := range names {
		this := got[i]
		// The positive: this cycle reported its own file.
		if !sawPath(this, own) {
			t.Fatalf("cycle %d did not report its own file %q: %v — this cycle observed "+
				"nothing, so what it left out proves nothing", i+1, own, this)
		}
		// The negative, read from the same slice: none of the earlier cycles'
		// files came back.
		for _, earlier := range names[:i] {
			if k := kindsFor(this, earlier); len(k) > 0 {
				t.Fatalf("cycle %d was handed %q again as %v: %v\nthe baseline stopped "+
					"advancing, so every later cycle re-reports the whole session's work",
					i+1, earlier, k, this)
			}
		}
	}
}
