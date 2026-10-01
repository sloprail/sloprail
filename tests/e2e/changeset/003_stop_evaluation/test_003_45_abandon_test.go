package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_45: a grounded abandon. A left branch is judged until the USER's own words drop it:
// no citation, or an assistant's words, do not; a user quote does, until the tip moves.
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
	if !strings.Contains(got, "feat-a") || !strings.Contains(got, "sr-session refs abandon") {
		t.Fatalf("the refusal must name the branch and mention the abandon option:\n%s", got)
	}
	blocks := stopBlocks(e, proj, sess)

	// No citation at all: refused, and the branch is still judged.
	e.Run(proj, sess, "nothing cited", Turns("done",
		Bash("a1", "sr-session refs abandon --ref feat-a"),
	))
	if n := stopBlocks(e, proj, sess); n <= blocks {
		t.Fatal("an abandon without a citation dropped the branch")
	}
	blocks = stopBlocks(e, proj, sess)

	// An assistant's words are not the user's: refused.
	e.Run(proj, sess, "assistant words", Turns("done",
		harness.Say("s1", "I decided the pineapple branch is abandoned by me"),
		Bash("a2", "sr-session refs abandon --ref feat-a --cite-user 'pineapple branch is abandoned by me'"),
	))
	if n := stopBlocks(e, proj, sess); n <= blocks {
		t.Fatal("an assistant's quote dropped the branch")
	}
	blocks = stopBlocks(e, proj, sess)

	// The user's own words: dropped.
	e.Run(proj, sess, "go on", Turns("done",
		Bash("a3", "sr-session refs abandon --ref feat-a --cite-user 'it is dead, do not keep it'"),
	))
	if n := stopBlocks(e, proj, sess); n != blocks {
		t.Fatalf("a user-cited abandon did not drop the branch:\n%s", newBlocks(e, proj, sess, blocks))
	}

	// The tip moves: judged again.
	e.Run(proj, sess, "one more commit", Turns("done",
		Bash("m1", "git switch -q feat-a"),
		harness.CommitFile("c2", "docs/a2.md", "FORBIDDEN again", "more on a"),
		Bash("m2", "git switch -q "+main),
	))
	if got := newBlocks(e, proj, sess, blocks); !strings.Contains(got, "feat-a") {
		t.Fatalf("a branch whose tip moved after the abandon was not judged again:\n%s", got)
	}
}
