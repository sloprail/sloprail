package e2e

import "testing"

// T005_01: a session in a repository records where it begins.
//
// Through the real wiring: the session starts, the harness fires SessionStart,
// the plugin runs `sloprail session start`, and the point lands in the
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
		Bash("b1", "git add -A && git commit -m 'agent commit'"),
	))

	if now := e.Git(proj, "rev-parse", "HEAD"); now == start {
		t.Fatalf("the agent did not actually commit, so this proves nothing")
	}
	if got := e.Meta(proj, sess, metaBaselineCommit); got != start {
		t.Fatalf("baseline commit = %q, want it still at the session's start (%q)", got, start)
	}
}

// T005_04: the agent switches branches, and the point follows.
//
// A point recorded on the line the tree left describes a history it no longer
// has, and the difference against it is every commit between the two — an
// entire branch delta arriving at one cycle as though this session had written
// it. The Stop hook is where that is noticed.
func TestT005_04_BranchSwitchRetakesThePoint(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	const sess = "s-005-04"
	e.Run(proj, sess, "switch branches", Turns("done",
		Bash("b1", "git checkout -b feature && git commit --allow-empty -m 'on feature'"),
	))

	if got := e.Git(proj, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
		t.Fatalf("the agent did not actually switch branches (on %q), so this proves nothing", got)
	}
	if got := e.Meta(proj, sess, metaBaselineBranch); got != "feature" {
		t.Fatalf("baseline branch = %q, want it to have followed the tree to %q", got, "feature")
	}
	if got, head := e.Meta(proj, sess, metaBaselineCommit), e.Git(proj, "rev-parse", "HEAD"); got != head {
		t.Fatalf("baseline commit = %q, want the point re-taken on the new line (%q)", got, head)
	}
}

// T005_05: a completed cycle remembers how far the record has been read.
//
// The mark is what lets the next cycle read only what has not been judged. It
// is written by the Stop hook, so what this proves is that a session ending
// leaves one behind at all.
func TestT005_05_CompletedCycleLeavesAReadMark(t *testing.T) {
	e := New(t)
	proj := e.Project()

	const sess = "s-005-05"
	e.Run(proj, sess, "do a thing", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	if got := e.Meta(proj, sess, metaTranscriptRead); got == "" {
		t.Fatal("a completed cycle left no read mark, so the next one would re-judge the whole session")
	}
}
