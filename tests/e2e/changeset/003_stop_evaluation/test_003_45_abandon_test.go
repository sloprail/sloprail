package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_45: an abandon. A left branch is judged until the agent untracks it with a reason (no reason
// does not drop it); the untracking holds only while the tip stays: new commits judge it again.
func TestT003_45_AGroundedAbandonDropsABranchUntilItsTipMoves(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	const sess = "s-003-45"

	e.Run(proj, sess, "this is the experiment branch feat-a, it is dead, do not keep it", Turns("done",
		Bash("b1", "git switch -q -c feat-a"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("b2", "git switch -q "+main),
	))
	got := stopRefusals(e, proj, sess)
	if !strings.Contains(got, "feat-a") || !strings.Contains(got, "sr-session refs untrack") {
		t.Fatalf("the refusal must name the branch and mention the abandon option:\n%s", got)
	}
	blocks := stopBlocks(e, proj, sess)

	// No reason at all: refused, and the branch is still judged.
	e.Run(proj, sess, "nothing said", Turns("done",
		Bash("a1", "sr-session refs untrack --head feat-a"),
	))
	if n := stopBlocks(e, proj, sess); n <= blocks {
		t.Fatal("an untrack without a reason dropped the branch")
	}
	blocks = stopBlocks(e, proj, sess)

	// With the reason: dropped.
	e.Run(proj, sess, "go on", Turns("done",
		Bash("a3", "sr-session refs untrack --head feat-a --reason 'the user said feat-a is dead, do not keep it'"),
	))
	if n := stopBlocks(e, proj, sess); n != blocks {
		t.Fatalf("an untracked branch was still judged:\n%s", newBlocks(e, proj, sess, blocks))
	}

	// The tip moves: judged again, by itself.
	e.Run(proj, sess, "one more commit", Turns("done",
		Bash("m1", "git switch -q feat-a"),
		harness.CommitFile("c2", "docs/a2.md", "FORBIDDEN again", "more on a"),
		Bash("m2", "git switch -q "+main),
	))
	if got := newBlocks(e, proj, sess, blocks); !strings.Contains(got, "feat-a") {
		t.Fatalf("a branch whose tip moved after the untrack was not judged again:\n%s", got)
	}
}
