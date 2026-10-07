package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_56: a force-move of a ref is not a commit, but under observation tracking a branch whose
// tip moved during the session onto commits that are not the remote's is the session's (the rule
// errs toward over-tracking: the tip is the evidence, not who made the commit). The session
// committed on branch X (clean), then reused the name: `git branch -f X foreign`, a branch
// somebody else made before the session began. X is judged at the foreign tip, and the agent,
// which did not make those commits, is released from them by untracking each range with a reason.
func TestT003_56_AForceMovedRefIsOverTrackedAndCanBeUntracked(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")

	e.Git(proj, "switch", "-q", "-c", "foreign")
	e.WriteFile(proj, "docs/foreign.md", "FORBIDDEN words\n")
	e.CommitAll(proj, "somebody else adds foreign")
	e.Git(proj, "switch", "-q", main)

	e.Run(proj, "s-003-56", "do the work, then reuse the branch name", Turns("done",
		Bash("b1", "git switch -q -c x"),
		harness.CommitFile("c1", "docs/x.md", "clean words", "add x"),
		Bash("b2", "git switch -q "+main),
		Bash("b3", "git branch -f x foreign"),
	))
	if got := stopRefusals(e, proj, "s-003-56"); !strings.Contains(got, refusalText) {
		t.Fatalf("a ref force-moved onto commits that are not the remote's was not over-tracked:\n%s", got)
	}

	seen := stopBlocks(e, proj, "s-003-56")
	e.Run(proj, "s-003-56", "untrack what is not mine", Turns("done",
		Bash("u1", "sr-session refs untrack --head x --reason 'a branch somebody else made, force-moved onto by mistake'"),
		Bash("u2", "sr-session refs untrack --head foreign --reason 'somebody else made it before this session'"),
	))
	if got := newBlocks(e, proj, "s-003-56", seen); strings.Contains(got, refusalText) {
		t.Fatalf("the untracked branches were still judged:\n%s", got)
	}
}

// T003_56: the same ref, moved by a plain commit on top of what the session recorded, is
// still the session's (the claim follows the commits, not the name).
func TestT003_56_ACommitOnTopOfARecordedRefIsStillJudged(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")

	e.Run(proj, "s-003-56b", "commit twice on x, leave it", Turns("done",
		Bash("b1", "git switch -q -c x"),
		harness.CommitFile("c1", "docs/x.md", "clean words", "add x"),
		harness.CommitFile("c2", "docs/x.md", "FORBIDDEN words", "break x"),
		Bash("b2", "git switch -q "+main),
	))
	if got := stopRefusals(e, proj, "s-003-56b"); !strings.Contains(got, refusalText) {
		t.Fatalf("a branch the session committed on was not judged:\n%s", got)
	}
}
