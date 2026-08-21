package e2e

import (
	"encoding/json"
	"testing"
)

// baseline_tracks_history: the point a cycle's difference is measured from
// describes the same line of history the tree is currently on.
//
// The spec's reasoning is a concrete disaster: "An agent may switch branches
// mid-session. A point recorded on the line it left describes a history the
// tree no longer has, and the difference against it is every commit between the
// two — hundreds of files this session never touched, arriving at the next cycle
// as though it had just written them."
//
// That is what this directory tests, and it is tested by CONSEQUENCE rather than
// by reading the recorded point. Where the baseline sits is checked on
// impl/baseline-mark (tests/e2e/session/005_baseline, T005_04/06/07) by reading
// the engine's own meta keys. Repeating that here would duplicate its coverage
// and prove nothing new.
//
// What is NOT covered there is the thing the invariant exists to prevent: the
// flood. 005 asserts the recorded commit follows the branch; this asserts that
// the files from the abandoned line never reach a guardrail. Those are different
// failures — an engine could re-take the point and still diff against the wrong
// thing, or take it correctly and dispatch from a stale cache.

// recordEverything is a NEW-FORMAT file-guard that records every after-the-fact
// file event it is handed and permits unconditionally. `match: "**/*.md"` selects
// every markdown file at any depth — the faithful stand-in for the old binding to
// all three after-the-fact kinds, which a single file-guard now covers because it
// fires on whichever Post kind the change produced. The ledger (`seen`, no `.md`)
// is not matched, so the guard cannot re-observe its own bookkeeping. Re-vehicled
// from the old GUARDRAIL.md hooks per tests/e2e/REVEHICLE-PATTERN.md so this
// coverage of the shared baseline/tree-diff machinery survives the old dispatch's
// deletion, observed through the new flat CheckPayload.
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

// T014_01: switching to another line of history does not deliver that line's
// files as this cycle's work.
//
// The branch `feature` is built off the ROOT commit, so it genuinely does not
// contain what `main` holds — the delta between the two is real and large
// enough to be unmistakable. `only-on-main.md` exists on main and not on
// feature; when the agent checks feature out, that file DISAPPEARS from the
// tree, and a difference measured from the point on main would report it as a
// deletion this session performed.
//
// The session also writes a file of its own, so the cycle is not empty and the
// assertion about the flood is made against a ledger proven to be live.
func TestT014_01_SwitchingBranchesDoesNotDeliverTheOtherLinesFiles(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": recordScript})

	// The guardrail is committed to the ROOT, before either branch diverges, so
	// it exists on both lines of history.
	//
	// Not incidental setup. `git checkout` of a branch cut from the root removes
	// every file that branch does not contain — including .sloprail/ — and a
	// project with no declarations loads no rules, so the hook cannot fire and
	// the ledger stays empty. That is indistinguishable from "the engine
	// correctly reported nothing", and it is how this test first passed for the
	// wrong reason. Committing the rule to the shared root is what keeps it
	// alive across the switch.
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the guardrail, on every line of history")
	root := e.Git(proj, "rev-parse", "HEAD")

	// A file that lives only on main. Committed on main, absent from feature.
	writeFile(t, proj, "only-on-main.md", "belongs to main\n")
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "a file only main has")

	// feature is cut from the commit holding only the guardrail, so it does not
	// contain only-on-main.md and the delta between the two lines is real.
	e.Git(proj, "checkout", "-b", "feature", root)
	writeFile(t, proj, "only-on-feature.md", "belongs to feature\n")
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "a file only feature has")
	e.Git(proj, "checkout", "main")

	// The premise this test rests on: the rule survived the round trip and is
	// present on the branch the session will switch to.
	if e.Git(proj, "cat-file", "-t", "feature:.sloprail/file-guard/watcher/file-guard.yaml") != "blob" {
		t.Fatalf("the guardrail is not present on the branch the agent switches to, so the " +
			"rule cannot fire there and an empty ledger would prove nothing")
	}

	e.Run(proj, "s-014-01", "switch branches and work", Turns("done",
		Bash("b1", "git checkout feature"),
		Write("w1", "my-own-work.md", "written by this session\n"),
	))

	if got := e.Git(proj, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
		t.Fatalf("the agent did not actually switch branches (on %q), so this proves nothing", got)
	}

	got := observedFiles(t, e.FileGuardLedgerLines(proj, "watcher", "seen"))
	// The control. The session's own file must be reported, or the two absences
	// below are just an engine that dispatched nothing.
	if !sawPath(got, "my-own-work.md") {
		t.Fatalf("the session's own file is missing from %v — nothing was observed at all, "+
			"so the assertions about the branch delta below cannot fail", got)
	}
	// The flood, in both directions. Neither of these files is this session's
	// work: one vanished because the tree moved, the other appeared for the same
	// reason.
	if sawPath(got, "only-on-main.md") {
		t.Fatalf("a file that exists only on the abandoned branch was reported as this cycle's work: %v — "+
			"the point is still on the line the tree left, and its whole delta is arriving as the session's", got)
	}
	if sawPath(got, "only-on-feature.md") {
		t.Fatalf("a file that arrived with the checked-out branch was reported as this cycle's work: %v — "+
			"the session did not write it, the branch switch brought it", got)
	}
}

// T014_02: a branch created off the session's own work keeps that work in the
// difference.
//
// The opposite error, and the reason the invariant says "the same line of
// history" rather than "re-measure whenever the branch name changes". `git
// checkout -b` moves no history at all — the tree is byte for byte what it was.
// An engine that re-took its point on every branch change would push the
// session's own committed work out of the difference through the branch door,
// losing exactly what the cycle is supposed to report.
//
// 005_baseline's T005_06 asserts the recorded commit does not move here. This
// asserts the consequence that matters: the work still reaches a guardrail.
func TestT014_02_ANewBranchOffOwnWorkKeepsItInTheDifference(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": recordScript})

	e.Run(proj, "s-014-02", "commit then branch", Turns("done",
		Write("w1", "session-work.md", "written by this session\n"),
		Bash("b1", "git add -A && git commit -m 'agent commit' && git checkout -b feature"),
	))

	if got := e.Git(proj, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
		t.Fatalf("the agent is on %q, not the new branch, so this proves nothing", got)
	}

	got := observedFiles(t, e.FileGuardLedgerLines(proj, "watcher", "seen"))
	if !sawPath(got, "session-work.md") {
		t.Fatalf("the session's own committed work vanished from the difference after `checkout -b`: %v — "+
			"a new branch moves no history, so re-measuring here drops the cycle's work through the branch door", got)
	}
}
