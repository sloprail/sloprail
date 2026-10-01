package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T005_01: a session in a repository records where it begins.
//
// Through the real wiring: the session starts, the harness fires SessionStart,
// the plugin runs `sr-session start`, and the point lands in the
// session's own record. A test that invoked the subcommand itself would prove
// the engine records correctly while proving nothing about whether the session
// beginning ever asks it to.
func TestT005_01_SessionStartRecordsTheBaseline(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	head := e.Git(proj, "rev-parse", "HEAD")

	const sess = "s-005-01"
	e.Run(proj, sess, "do a thing", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	if got := e.Meta(proj, sess, metaBaselineCommit); got != head {
		t.Fatalf("baseline commit = %q, want the commit HEAD points at (%q)", got, head)
	}
	// The branch is not decoration: it is the only thing that makes a switch to
	// another line of history noticeable.
	if got := e.Meta(proj, sess, metaBaselineBranch); got != "main" {
		t.Fatalf("baseline branch = %q, want %q", got, "main")
	}
}

// T005_02: a project that is not a repository still starts.
//
// A session that cannot start because of the engine's own bookkeeping is worse
// than a session with no baseline. The Project() the harness makes is not a
// repository, so this is the plain case.
func TestT005_02_NoRepositoryStillStarts(t *testing.T) {
	e := New(t)
	proj := e.Project()

	got := e.Run(proj, "s-005-02", "do a thing", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	if got.Code != 0 {
		t.Fatalf("the session did not run to completion (exit %d):\n%s", got.Code, got.Output)
	}
	if v := e.Meta(proj, "s-005-02", metaBaselineCommit); v != "" {
		t.Fatalf("a non-repository recorded a baseline commit %q", v)
	}
}

// T005_03: an agent that commits during the session does not move the point.
//
// The point is where the SESSION began, not where HEAD is. Following HEAD would
// let an agent push its own work — including work a hook refused — out of the
// difference simply by committing it.
func TestT005_03_CommittingDoesNotMoveThePoint(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	start := e.Git(proj, "rev-parse", "HEAD")

	const sess = "s-005-03"
	e.Run(proj, sess, "commit something", Turns("done",
		Write("w1", "notes.md", "hello"),
		harness.Commit("b1", "agent commit"),
	))

	if now := e.Git(proj, "rev-parse", "HEAD"); now == start {
		t.Fatalf("the agent did not actually commit, so this proves nothing")
	}
	if got := e.Meta(proj, sess, metaBaselineCommit); got != start {
		t.Fatalf("baseline commit = %q, want it still at the session's start (%q)", got, start)
	}
}

// T005_04: the agent leaves for another line of history, and the point follows.
//
// A point recorded on the line the tree left describes a history it no longer
// has, and the difference against it is every commit between the two — an
// entire branch delta arriving at one cycle as though this session had written
// it. The Stop hook is where that is noticed.
//
// The branch it switches to is prepared off the root, so it genuinely does not
// contain the session's point. Switching to a branch created from where the
// tree already is leaves nothing behind, and is T005_06.
func TestT005_04_LeavingTheHistoryRetakesThePoint(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Git(proj, "commit", "--allow-empty", "-m", "second on main")

	root := e.Git(proj, "rev-list", "--max-parents=0", "HEAD")
	e.Git(proj, "checkout", "-b", "feature", root)
	e.Git(proj, "commit", "--allow-empty", "-m", "on feature")
	featureTip := e.Git(proj, "rev-parse", "HEAD")
	e.Git(proj, "checkout", "main")

	const sess = "s-005-04"
	e.Run(proj, sess, "switch branches", Turns("done",
		Bash("b1", "git checkout feature"),
	))

	if got := e.Git(proj, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
		t.Fatalf("the agent did not actually switch branches (on %q), so this proves nothing", got)
	}
	if got := e.Meta(proj, sess, metaBaselineBranch); got != "feature" {
		t.Fatalf("baseline branch = %q, want it to have followed the tree to %q", got, "feature")
	}
	if got := e.Meta(proj, sess, metaBaselineCommit); got != featureTip {
		t.Fatalf("baseline commit = %q, want the point re-taken on the new line (%q)", got, featureTip)
	}
}

// T005_06: a new branch off the session's own work does not move the point.
//
// `git checkout -b` changes the name and moves no history. Re-taking the point
// here would push the agent's own committed work out of the difference through
// the branch door — the same loss the record-once rule exists to prevent,
// reached by an operation that changed no history at all.
func TestT005_06_NewBranchOffOwnWorkDoesNotMoveThePoint(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	start := e.Git(proj, "rev-parse", "HEAD")

	const sess = "s-005-06"
	e.Run(proj, sess, "commit then branch", Turns("done",
		Write("w1", "notes.md", "hello"),
		harness.Commit("b1", "agent commit"),
		Bash("b2", "git checkout -b feature"),
	))

	if got := e.Git(proj, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
		t.Fatalf("the agent is on %q, not on the new branch, so this proves nothing", got)
	}
	if now := e.Git(proj, "rev-parse", "HEAD"); now == start {
		t.Fatalf("the agent did not actually commit, so this proves nothing")
	}
	if got := e.Meta(proj, sess, metaBaselineCommit); got != start {
		t.Fatalf("baseline commit = %q, want it still at the session's start (%q) — "+
			"the agent's own commit must stay inside the difference", got, start)
	}
}

// T005_07: renaming the branch does not move the point.
//
// Same commit, same history, a different name. Nothing was left, so there is
// nothing to re-measure from.
func TestT005_07_RenamingTheBranchDoesNotMoveThePoint(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	start := e.Git(proj, "rev-parse", "HEAD")

	const sess = "s-005-07"
	e.Run(proj, sess, "rename the branch", Turns("done",
		Write("w1", "notes.md", "hello"),
		harness.Commit("b1", "agent commit"),
		Bash("b2", "git branch -m main trunk"),
	))

	if got := e.Git(proj, "rev-parse", "--abbrev-ref", "HEAD"); got != "trunk" {
		t.Fatalf("the branch was not actually renamed (on %q), so this proves nothing", got)
	}
	if got := e.Meta(proj, sess, metaBaselineCommit); got != start {
		t.Fatalf("baseline commit = %q, want it still at the session's start (%q) — "+
			"a rename is not a change of history", got, start)
	}
}

// T005_05: a cycle that judged nothing marks nothing as judged.
//
// The mark asserts a position has been judged, so it may only move over work
// something actually looked at. This session has no guardrail reading the
// record, and the Post events are not dispatched yet — so nothing judged
// anything, and the mark must stay empty.
//
// The inverse of the test that used to be here, which asserted a mark existed
// after any session at all. That was the F1 bug stated as a requirement: it
// passed because the mark was re-derived from the record at write time, which
// is exactly how turns nothing had seen were being marked judged.
func TestT005_05_ACycleThatJudgedNothingLeavesNoReadMark(t *testing.T) {
	e := New(t)
	proj := e.Project()

	const sess = "s-005-05"
	e.Run(proj, sess, "do a thing", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	if got := e.Meta(proj, sess, metaTranscriptRead); got != "" {
		t.Fatalf("read mark = %q, want none — nothing judged this session, so nothing may be "+
			"marked judged; a mark here means the next cycle silently skips those turns", got)
	}
}
