package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// What a file's history across CYCLES looks like to the rules bound to it.
//
// THE MODEL, stated once here because getting it wrong makes these tests assert
// the opposite of the truth. A file-guard judges a RANGE of commits: from where
// the rule last passed (its watermark) to HEAD, as ONE squashed change. So:
//
//   - a cycle whose changeset the rule passed moves the rule's base to that head:
//     the next cycle's range is only the commits made since, and a file this
//     session created in an earlier cycle is, by then, part of the base (an edit
//     is a modification, not a second creation);
//   - within one range only the NET change counts: a file created and removed, or
//     removed and put back byte for byte, is no change at all, however many
//     commits the agent made along the way.
//
// Each test drives several Run calls under ONE session id, which is what makes
// them cycles of one session. The ledger accumulates across them, so every
// assertion reads the DELTA a cycle added rather than a total.
//
// The recorder writes its ledger OUTSIDE the project. The rule's verdicts are
// keyed on a hash of its whole folder, so a check appending to a file inside its
// own folder would be a different rule at every Stop and its watermark would
// never hold.

// recordEverything is a file-guard that records the changeset it is handed and
// passes it. `deletions: include` — this guard observes EVERY change.
const recordEverything = `match: "**/*.md"
deletions: include
checks:
  - script: ./record.sh
`

// observed is one file a recorded changeset selected.
type observed struct {
	Status string
	Path   string
}

// observedFiles decodes what a file-guard's check was handed: the Changeset
// payload's files.
func observedFiles(t *testing.T, lines []string) []observed {
	t.Helper()
	var got []observed
	for _, line := range lines {
		var p struct {
			Changeset struct {
				Files []struct {
					Path   string `json:"path"`
					Status string `json:"status"`
				} `json:"files"`
			} `json:"changeset"`
		}
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("the check was handed something that is not a changeset payload: %v\n%s", err, line)
		}
		for _, f := range p.Changeset.Files {
			got = append(got, observed{Status: f.Status, Path: f.Path})
		}
	}
	return got
}

// statusesFor is every status reported for one path, in arrival order.
func statusesFor(got []observed, path string) []string {
	var out []string
	for _, o := range got {
		if o.Path == path {
			out = append(out, o.Status)
		}
	}
	return out
}

func sawPath(got []observed, path string) bool { return len(statusesFor(got, path)) > 0 }

// cycler drives cycles of one session and reads what the rule was handed.
type cycler struct {
	e      *harness.Env
	proj   string
	ledger string
	seen   int
}

// project is a repository with the recording guardrail already committed, so the
// rule's own folder is part of the history the first range starts from. seed is
// the project's own files (path to content), committed in that same commit: a
// file committed AFTER the rule is part of the rule's first range, not of its base.
func project(t *testing.T, seed map[string]string) (*harness.Env, string, *cycler) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	for path, body := range seed {
		e.WriteFile(proj, path, body)
	}
	ledger := filepath.Join(t.TempDir(), "seen")
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{
		"record.sh": "#!/bin/sh\ncat >> " + ledger + "\necho >> " + ledger + "\nexit 0\n",
	})
	e.CommitAll(proj, "the project before the session")
	return e, proj, &cycler{e: e, proj: proj, ledger: ledger}
}

// cycle runs one scenario (the agent commits its work at the end) and returns
// only the files the rule was handed THIS cycle.
//
// The per-cycle slicing is the whole helper: totals cannot tell "cycle two
// reported it" from "cycle one reported it twice".
func (c *cycler) cycle(t *testing.T, sess string, s harness.Scenario) []observed {
	t.Helper()
	c.e.Run(c.proj, sess, "cycle", s.ThenCommit("the cycle"))
	body, _ := os.ReadFile(c.ledger)
	var lines []string
	for _, l := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) < c.seen {
		t.Fatalf("the ledger shrank (%d lines, was %d)", len(lines), c.seen)
	}
	out := observedFiles(t, lines[c.seen:])
	c.seen = len(lines)
	return out
}

// T022_01: a file created in one cycle and edited in the next is an ADD, then a
// MODIFICATION.
//
// The rule passed cycle one's changeset, so its base moved to that head and the
// file is part of the base for cycle two: the edit is measured against it.
func TestT022_01_AFileThisSessionCreatedIsAnUpdateInTheNextCycle(t *testing.T) {
	_, _, c := project(t, nil)

	first := c.cycle(t, "s-022-01", Turns("done", Write("w1", "subject.md", "first version\n")))
	if k := statusesFor(first, "subject.md"); len(k) == 0 || k[0] != "A" {
		t.Fatalf("cycle one did not report the new file as added: %v (all: %v)", k, first)
	}
	second := c.cycle(t, "s-022-01", Turns("done", Write("w2", "subject.md", "second version\n")))
	k := statusesFor(second, "subject.md")
	if len(k) == 0 {
		t.Fatalf("cycle two never reported the file it edited: %v", second)
	}
	if k[0] != "M" {
		t.Fatalf("cycle two reported %v for a file the rule had already passed as added; want M — "+
			"the rule's base did not move to the head it passed", k)
	}
}

// T022_01b: a file present before the session is modified in every cycle that
// edits it.
//
// The other side of T022_01, and what stops it being read as "everything is an
// add": the discriminator is whether the path is in the base, not the cycle count.
func TestT022_01b_AFileAtTheBaseIsAnUpdateInEveryCycle(t *testing.T) {
	_, _, c := project(t, map[string]string{"pre-existing.md": "original\n"})

	for i, body := range []string{"edited once\n", "edited twice\n"} {
		this := c.cycle(t, "s-022-01b", Turns("done", Write("w"+string(rune('1'+i)), "pre-existing.md", body)))
		k := statusesFor(this, "pre-existing.md")
		if len(k) == 0 {
			t.Fatalf("cycle %d never reported the file it edited: %v", i+1, this)
		}
		if k[0] != "M" {
			t.Fatalf("cycle %d reported %v for a file in the base; want M", i+1, k)
		}
	}
}

// T022_02: a file created and deleted within ONE range is reported as neither.
//
// The agent commits the file, then removes it and commits again: the squashed
// change of the range holds no trace of it, so there is no content to judge. The
// control is a second file the same range leaves in place, so "nothing was
// reported" cannot pass on a range that dispatched nothing at all.
func TestT022_02_CreatedAndDeletedInTheSameRangeIsSilent(t *testing.T) {
	_, _, c := project(t, nil)

	only := c.cycle(t, "s-022-02", Turns("done",
		Write("w1", "ephemeral.md", "here and gone\n"),
		Write("w2", "survivor.md", "still here\n"),
		harness.Commit("mid", "both files"),
		Bash("b1", "rm ephemeral.md"),
	))

	if !sawPath(only, "survivor.md") {
		t.Fatalf("the file the range left behind was not reported: %v — nothing was observed, "+
			"so the silence about the ephemeral file proves nothing", only)
	}
	if k := statusesFor(only, "ephemeral.md"); len(k) > 0 {
		t.Fatalf("a file created and removed within the range was reported as %v: %v", k, only)
	}
}

// T022_03: a file deleted in one cycle and recreated in the next is a delete,
// then an add — each measured against what the rule last passed.
//
// Cycle one removes a file that was in the base: a delete. The rule passes it, so
// the base moves to a head without the file; cycle two puts the path back with
// different content, which is new relative to that base: an add.
func TestT022_03_DeletedThenRecreatedIsADeleteThenAnAdd(t *testing.T) {
	_, _, c := project(t, map[string]string{"revenant.md": "original\n"})

	first := c.cycle(t, "s-022-03", Turns("done", Bash("b1", "rm revenant.md")))
	if k := statusesFor(first, "revenant.md"); len(k) == 0 || k[0] != "D" {
		t.Fatalf("cycle one did not report the removal as a delete: %v (all: %v)", k, first)
	}
	second := c.cycle(t, "s-022-03", Turns("done", Write("w1", "revenant.md", "back again, and different\n")))
	if k := statusesFor(second, "revenant.md"); len(k) == 0 || k[0] != "A" {
		t.Fatalf("cycle two reported %v for a path the passed base no longer holds; want A "+
			"(all: %v)", k, second)
	}
}

// T022_03b: a file removed and restored with EXACTLY its old bytes within one
// range falls silent.
//
// The boundary of the case above, and the one that separates a net change from a
// log of what happened. The tree at HEAD matches the base for that path, so there
// is nothing to report. The control is a second file the same range changes.
func TestT022_03b_RestoredWithItsBytesWithinARangeIsSilent(t *testing.T) {
	const original = "original\n"
	_, _, c := project(t, map[string]string{"round-trip.md": original})

	got := c.cycle(t, "s-022-03b", Turns("done",
		Bash("b1", "rm round-trip.md"),
		harness.Commit("mid", "the file is gone"),
		Write("w1", "round-trip.md", original),
		Write("w2", "genuinely-changed.md", "new work\n"),
	))

	if !sawPath(got, "genuinely-changed.md") {
		t.Fatalf("the range reported nothing at all: %v — the silence about the restored file "+
			"would prove nothing", got)
	}
	if k := statusesFor(got, "round-trip.md"); len(k) > 0 {
		t.Fatalf("a file restored to its base bytes was reported as %v: %v", k, got)
	}
}

// T022_04: a file left alone after the rule passed it is not put in front of the
// rule again.
//
// The range starts where the rule last passed, so cycle two's changeset is only
// what was committed since. Both assertions are read from the same slice: the
// positive (the file cycle two edited) and the negative (the one it did not).
func TestT022_04_AFileLeftAloneAfterBeingPassedFallsSilent(t *testing.T) {
	_, _, c := project(t, nil)

	first := c.cycle(t, "s-022-04", Turns("done",
		Write("w1", "kept.md", "one\n"),
		Write("w2", "moved-on.md", "one\n"),
	))
	second := c.cycle(t, "s-022-04", Turns("done", Write("w3", "kept.md", "two\n")))

	if !sawPath(first, "kept.md") || !sawPath(first, "moved-on.md") {
		t.Fatalf("cycle one did not report both files: %v", first)
	}
	if !sawPath(second, "kept.md") {
		t.Fatalf("cycle two did not report the file it edited: %v — nothing was observed, so "+
			"the silence about the other file proves nothing", second)
	}
	if k := statusesFor(second, "moved-on.md"); len(k) > 0 {
		t.Fatalf("a file untouched since the rule passed it was reported again as %v: %v", k, second)
	}
}

// T022_05: five cycles in one session, each putting only its own work in front of
// the rule.
//
// The compounding case: without a base that moves, by cycle five the rule is
// handed five files where one belongs. Each cycle is checked as it goes, so a
// failure names the cycle the base stopped moving on.
func TestT022_05_FiveCyclesEachReportOnlyTheirOwnWork(t *testing.T) {
	_, _, c := project(t, nil)

	names := []string{"c1.md", "c2.md", "c3.md", "c4.md", "c5.md"}
	for i, own := range names {
		this := c.cycle(t, "s-022-05", Turns("done", Write(strings.Repeat("w", i+1), own, "written in cycle\n")))
		if !sawPath(this, own) {
			t.Fatalf("cycle %d did not report its own file %q: %v", i+1, own, this)
		}
		for _, earlier := range names[:i] {
			if k := statusesFor(this, earlier); len(k) > 0 {
				t.Fatalf("cycle %d was handed %q again as %v: %v — the base stopped advancing", i+1, earlier, k, this)
			}
		}
	}
}
