package e2e

import (
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_81: a branch's range belongs to ONE home folder of the repository: the folder that has it
// checked out, else the one already holding its row (the root first), else the root. A sibling
// worktree sees every branch in the shared ref namespace, but never answers for one it is not the
// home of. #198 kept out only a branch checked out in ANOTHER worktree; a branch checked out
// nowhere, or one made by a sibling, was still attached to every folder (one real session: 4,082
// rows over 111 folders). Boundary cases covered here: a branch moved in one worktree while N
// siblings can see it, a branch checked out nowhere, a sub-agent worktree (its own branch and one it
// left), a branch handed to another worktree, an untrack followed by a tip move. Nothing the session
// worked on is dropped: every branch stays tracked in SOME folder.
func TestT003_81_ABranchIsNotAttachedToSiblingWorktrees(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	const sess = "s-003-81"
	main := e.Git(proj, "branch", "--show-current")
	third := filepath.Join(t.TempDir(), "third-tree")

	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b0", "git switch -q -c sub-left"),
		harness.CommitFile("c0", "docs/e.md", "sub words", "sub adds e"),
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
	// Nothing dropped: a sub-agent's branch it left (checked out nowhere) is still answered for.
	if !trackedIn(rs, wt, "sub-left") && !trackedIn(rs, proj, "sub-left") {
		t.Fatalf("the sub-agent's branch sub-left, which it left, is tracked in no folder: %+v", rs)
	}
	if !trackedIn(rs, wt, "sub-own") {
		t.Fatalf("premise: the sub-agent's own branch is not tracked in its folder: %+v", rs)
	}
	for _, head := range []string{"sub-own", "sub-left"} {
		if trackedIn(rs, proj, head) && trackedIn(rs, wt, head) {
			t.Fatalf("%s is answered for in both the root's checkout and the sub-agent's worktree: %+v", head, rs)
		}
	}

	// An untrack, then the tip moves: the branch is tracked again by itself, by its home, and
	// the sibling worktree still does not pick it up.
	e.Run(proj, sess, "root gives up", Turns("root done",
		Bash("u1", "sr-session refs untrack --head root-left --reason 'the user said root-left is dead, do not keep it'"),
	))
	e.Run(proj, sess, "root more", Turns("root done",
		Bash("m1", "git switch -q root-left"),
		harness.CommitFile("m2", "docs/d.md", "more words", "root adds d"),
		Bash("m3", "git switch -q "+main),
	))
	e.StopNow(proj, sess, false)
	rs = sessionRanges(t, e, proj, sess)
	if trackedIn(rs, wt, "root-left") {
		t.Fatalf("root-left moved after an untrack and was attached to the sibling worktree: %+v", rs)
	}
	if !trackedIn(rs, proj, "root-left") {
		t.Fatalf("root-left moved after an untrack and is tracked again nowhere: %+v", rs)
	}
}
