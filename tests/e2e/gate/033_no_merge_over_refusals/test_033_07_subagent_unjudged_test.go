package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T033_07: a tip recorded for a sub-agent whose Stop has not judged it blocks the merge
// (the coordinator's own Stop does not judge another agent's work); the same agent's work,
// once ITS Stop has passed, does not.
func TestT033_07_ASubagentsUnjudgedTipBlocksTheMergeUntilItsStopPasses(t *testing.T) {
	const sess = "s-033-07"
	e, proj := unjudgedProject(t)
	setPR(t, e, "9", "sub-u")
	setPR(t, e, "10", "sub-ok")
	main := e.Git(proj, "branch", "--show-current")
	e.Run(proj, sess, "begin", Turns("started"))

	// A branch with a clean commit, recorded as a sub-agent's, that its Stop has not judged.
	e.Git(proj, "switch", "-q", "-c", "sub-u")
	e.WriteFile(proj, "docs/u.md", "clean words\n")
	tip := e.CommitAll(proj, "sub adds u")
	e.Git(proj, "switch", "-q", main)
	id := e.SessionIdentity(proj, sess)
	if out := e.CLIDirect(proj, "sr-session", "refs", "add", "--session", id, "--folder", proj,
		"--ref", "sub-u", "--tip", tip, "--agent", "sub-1"); out.Code != 0 {
		t.Fatalf("premise: recording the sub-agent's ref:\n%s", out.Output)
	}
	res := e.Run(proj, sess, "merge 9", Turns("done", Bash("m1", "gh pr merge 9 --squash --admin")))
	if !res.Refused() || !res.Saw("end your turn") {
		t.Fatalf("the coordinator merged a sub-agent's unjudged tip:\n%s", res.Output)
	}

	// A sub-agent whose Stop runs and passes: its branch merges.
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-ok"),
		harness.CommitFile("c1", "docs/ok.md", "clean words", "sub adds ok"),
	))
	e.Run(proj, sess, "delegate", Turns("root done", harness.Dispatch("d1", "write the docs", sub, "worktree")))
	res = e.Run(proj, sess, "merge 10", Turns("done", Bash("m2", "gh pr merge 10 --squash --admin")))
	if res.Saw("no-merge-over-refusals") {
		t.Fatalf("a branch whose sub-agent's Stop passed was refused:\n%s", res.Output)
	}
}
