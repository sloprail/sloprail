package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_61: a violating commit on a branch the agent then leaves is rewritten before any Stop
// has judged it, in every way git offers: a soft reset and recommit, an amend, a rebase onto a
// newer main, a branch moved with update-ref to a commit made by plumbing. A rewrite changes
// SHAs and topology, never the content: the violation still stands on the tracked branch, so
// Stop refuses it; fixing it there passes.
func TestT003_61_RewritingAnUnjudgedViolationHidesNothing(t *testing.T) {
	cases := []struct {
		name    string
		rewrite string // run on `side`, which holds the violating commit as HEAD
		final   string // the branch that holds the content afterwards
	}{
		{"soft reset and recommit", "git reset -q --soft HEAD~1 && git commit -q -m recommitted", "side"},
		{"amend", "git commit -q --amend -m reworded", "side"},
		{"rebase onto a newer main", "git switch -q main && git commit -q --allow-empty -m 'main moves' && git switch -q side && git rebase -q main", "side"},
		{"update-ref to a plumbed commit", "git update-ref refs/heads/side $(git commit-tree HEAD^{tree} -p HEAD~1 -m plumbed)", "side"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, proj, _ := project(t, docsRule)
			main := e.Git(proj, "branch", "--show-current")
			sess := "s-003-61-" + string(rune('a'+i))

			e.Run(proj, sess, "commit, then rewrite", Turns("done",
				Bash("b1", "git switch -q -c side && sr-session refs track --head side"),
				harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
				Bash("b2", c.rewrite),
				Bash("b3", "git switch -q "+main),
			))
			got := stopRefusals(e, proj, sess)
			if !strings.Contains(got, refusalText) || !strings.Contains(got, "docs/a.md") {
				t.Fatalf("the rewritten violation escaped judgement; refusals:\n%s", got)
			}
			blocks := stopBlocks(e, proj, sess)

			e.Run(proj, sess, "fix it", Turns("fixed",
				Bash("b4", "git switch -q "+c.final),
				harness.CommitFile("c2", "docs/a.md", "clean words", "fix a"),
				Bash("b5", "git switch -q "+main),
			))
			if n := stopBlocks(e, proj, sess); n != blocks {
				t.Fatalf("the fixed branch was still refused:\n%s", newBlocks(e, proj, sess, blocks))
			}
		})
	}
}
