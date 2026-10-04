package e2e

import (
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_81: a branch's range belongs to the folder where it is (or was) checked out or committed. A
// sibling worktree of the same repository sees the branch in the shared ref namespace, but it
// never answers for it: not for a branch the root committed on and left checked out nowhere, nor for
// one now checked out in another worktree. (An untrack is undone only by the range being re-tracked,
// so a range that is never attached to the sibling cannot come back there.)
func TestT003_81_ABranchIsNotAttachedToSiblingWorktrees(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	const sess = "s-003-81"
	main := e.Git(proj, "branch", "--show-current")
	third := filepath.Join(t.TempDir(), "third-tree")

	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-own"),
		harness.CommitFile("c1", "docs/a.md", "sub words", "sub adds a"),
	))
	e.Run(proj, sess, "delegate", Turns("root done", harness.Dispatch("d1", "write the docs", sub, "worktree")))
	wt, _ := subagentFolder(t, e, proj, sess)

	// The root commits on two branches of its own in its own checkout, then leaves them: one
	// checked out nowhere, one handed to a worktree the session never works in.
	e.Run(proj, sess, "root work", Turns("root done",
		Bash("r1", "git switch -q -c root-left"),
		harness.CommitFile("r2", "docs/b.md", "root words", "root adds b"),
		Bash("r3", "git switch -q -c root-held"),
		harness.CommitFile("r4", "docs/c.md", "root words", "root adds c"),
		Bash("r5", "git switch -q "+main+" && git worktree add -q "+third+" root-held"),
	))
	e.StopNow(proj, sess, false)

	rs := sessionRanges(t, e, proj, sess)
	for _, head := range []string{"root-left", "root-held"} {
		// A branch handed to another worktree is answered for there (the root's row is pruned).
		if !trackedIn(rs, proj, head) && !trackedIn(rs, third, head) {
			t.Fatalf("premise: the root's own branch %s is tracked neither in the root folder nor where it is checked out: %+v", head, rs)
		}
		if trackedIn(rs, wt, head) {
			t.Fatalf("%s, committed in the root's checkout, is attached to the sub-agent's sibling worktree: %+v", head, rs)
		}
	}
	if !trackedIn(rs, wt, "sub-own") {
		t.Fatalf("premise: the sub-agent's own branch is not tracked in its folder: %+v", rs)
	}
	if trackedIn(rs, proj, "sub-own") {
		t.Fatalf("the sub-agent's branch is attached to the root's checkout: %+v", rs)
	}

	// A branch that moves later stays with its home: the sub-agent's worktree does not pick it up.
	e.Run(proj, sess, "root more", Turns("root done",
		Bash("m1", "git switch -q root-left"),
		harness.CommitFile("m2", "docs/d.md", "more words", "root adds d"),
		Bash("m3", "git switch -q "+main),
	))
	e.StopNow(proj, sess, false)
	rs = sessionRanges(t, e, proj, sess)
	if trackedIn(rs, wt, "root-left") {
		t.Fatalf("root-left moved again and was re-attached to the sibling worktree: %+v", rs)
	}
}
