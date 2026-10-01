package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// landUpstream is a pull request merge: a squash commit with the branch's tree appears on
// the remote's default branch (as `gh pr merge --squash` makes it, not by any commit of this
// folder), and the remote-tracking ref moves to it.
func landUpstream(id, branch string) harness.Turn {
	return Bash(id, "git update-ref refs/remotes/origin/main \"$(git commit-tree "+branch+"^{tree} -p refs/remotes/origin/main -m 'squash "+branch+"')\"")
}

// T003_49: landing is no judgement. A branch the session committed on that was
// squash-merged upstream BEFORE any rule passed it is still owed: its next Stop judges it
// (and refuses a violation), exactly as if it had never landed. Only a branch a rule
// already passed is skipped once it has landed (T003_42, T003_44).
func TestT003_49_ABranchSquashMergedBeforeItWasJudgedIsStillJudged(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	e.Git(proj, "update-ref", "refs/remotes/origin/main", "HEAD")

	e.Run(proj, "s-003-49", "merge it before any Stop", Turns("done",
		Bash("b1", "git switch -q -c landed"),
		harness.CommitFile("c1", "docs/landed.md", "FORBIDDEN words", "add landed"),
		Bash("b2", "git switch -q "+main),
		landUpstream("s1", "landed"),
	))
	got := stopRefusals(e, proj, "s-003-49")
	if !strings.Contains(got, refusalText) || !strings.Contains(got, "landed") {
		t.Fatalf("a branch squash-merged before any rule passed it was skipped as landed; refusals:\n%s", got)
	}
	blocks := stopBlocks(e, proj, "s-003-49")

	// The fix goes on the branch; its new tip is judged and passes.
	e.Run(proj, "s-003-49", "fix it", Turns("fixed",
		Bash("b3", "git switch -q landed"),
		harness.CommitFile("c2", "docs/landed.md", "clean words", "fix landed"),
		Bash("b4", "git switch -q "+main),
	))
	if n := stopBlocks(e, proj, "s-003-49"); n != blocks {
		t.Fatalf("the fixed branch was still refused:\n%s", newBlocks(e, proj, "s-003-49", blocks))
	}
}

// T003_49: the same for a branch deleted after it landed, which nothing holds any more.
func TestT003_49_ADeletedSquashMergedBranchIsStillJudged(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	e.Git(proj, "update-ref", "refs/remotes/origin/main", "HEAD")

	e.Run(proj, "s-003-49d", "merge and delete it", Turns("done",
		Bash("b1", "git switch -q -c landed"),
		harness.CommitFile("c1", "docs/landed.md", "FORBIDDEN words", "add landed"),
		Bash("b2", "git switch -q "+main),
		landUpstream("s1", "landed"),
		Bash("b3", "git branch -q -D landed"),
	))
	got := stopRefusals(e, proj, "s-003-49d")
	if !strings.Contains(got, refusalText) {
		t.Fatalf("a squash-merged branch that was then deleted escaped judgement; refusals:\n%s", got)
	}
}

// T003_49: a sub-agent's own worktree, a branch it left and squash-merged before its Stop.
func TestT003_49_ASubagentBranchSquashMergedBeforeItsStopIsStillJudged(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	e.Git(proj, "update-ref", "refs/remotes/origin/main", "HEAD")
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-a"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "sub adds a"),
		Bash("b2", "git switch -q -c sub-b HEAD~1"),
		harness.CommitFile("c2", "docs/b.md", "clean words", "sub adds b"),
		landUpstream("s1", "sub-a"),
	))
	res := e.Run(proj, "s-003-49s", "delegate", Turns("root done",
		harness.Dispatch("d1", "write the docs", sub, "worktree"),
	))
	if !(strings.Contains(res.Output, "SubagentStop blocked (") && strings.Contains(res.Output, "On branch sub-a")) {
		t.Fatalf("a sub-agent's squash-merged branch was not judged at its Stop:\n%s", res.Output)
	}
}
