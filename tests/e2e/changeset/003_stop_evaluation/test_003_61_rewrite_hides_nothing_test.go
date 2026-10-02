package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_61: a violating commit on a branch the agent then leaves is rewritten before any Stop
// has judged it, in every way git offers: a soft reset and recommit, an amend, a rebase onto a
// newer main, a branch moved with update-ref to a commit made by plumbing, a branch renamed,
// the commit cherry-picked to a new branch and the old one deleted. A rewrite changes SHAs and
// topology, never the content: the violation still stands on a branch, so Stop refuses it;
// fixing it there passes.
func TestT003_61_RewritingAnUnjudgedViolationHidesNothing(t *testing.T) {
	cases := []struct {
		name    string
		rewrite string // run on `side`, which holds the violating commit as HEAD
		final   string // the branch that holds the content afterwards
		dead    string // a deleted branch whose original tip nothing holds, left owed until it is untracked
	}{
		{"soft reset and recommit", "git reset -q --soft HEAD~1 && git commit -q -m recommitted", "side", ""},
		{"amend", "git commit -q --amend -m reworded", "side", ""},
		{"rebase onto a newer main", "git switch -q main && git commit -q --allow-empty -m 'main moves' && git switch -q side && git rebase -q main", "side", ""},
		{"update-ref to a plumbed commit", "git update-ref refs/heads/side $(git commit-tree HEAD^{tree} -p HEAD~1 -m plumbed)", "side", ""},
		{"branch renamed", "git branch -m side renamed", "renamed", ""},
		{"cherry-pick to a new branch, old one deleted", "git switch -q -c moved main && git cherry-pick side && git switch -q main && git branch -D side", "moved", "side"},
		{"checkout -B onto a copy", "git checkout -q -B copy side && git branch -f side main", "copy", ""},
		// Nothing but the engine remembers the commit: the branch is reset away, the reflog
		// expired, and a branch is recreated at the SHA the agent noted.
		{"reset away, reflog expired, branch recreated at the old SHA",
			"git rev-parse HEAD > .git/old-tip && git reset -q --hard main && git reflog expire --expire=now --all && git branch copy $(cat .git/old-tip)", "copy", ""},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, proj, _ := project(t, docsRule)
			main := e.Git(proj, "branch", "--show-current")
			sess := "s-003-61-" + string(rune('a'+i))

			e.Run(proj, sess, "commit, then rewrite", Turns("done",
				Bash("b1", "git switch -q -c side"),
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
			if c.dead != "" {
				// The old tip is a deleted branch's: owed until it is untracked, with a reason.
				blocks = stopBlocks(e, proj, sess)
				e.Run(proj, sess, "the "+c.dead+" branch is dead, the work moved", Turns("done",
					Bash("a1", "sr-session refs untrack --head "+c.dead+" --reason 'the "+c.dead+" branch is dead, the work moved'"),
				))
			}
			if n := stopBlocks(e, proj, sess); n != blocks {
				t.Fatalf("the fixed branch was still refused:\n%s", newBlocks(e, proj, sess, blocks))
			}
		})
	}
}
