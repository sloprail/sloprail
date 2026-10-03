package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_80: a sub-agent's worktree folder still exists but its branch is gone. The Stop's advice,
// `sr-session refs untrack`, must work for that range: the head no longer resolves, so the range
// is found by the folder and head it was stored under.
func TestT003_80_AnUntrackNamesARangeWhoseBranchIsGoneInAFolderThatStands(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	const sess = "s-003-80"
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-gone-branch"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "sub adds a"),
	))
	e.Run(proj, sess, "delegate", Turns("root done", harness.Dispatch("d1", "write the docs", sub, "worktree")))
	wt, _ := subagentFolder(t, e, proj, sess)

	e.Git(wt, "switch", "-q", "--detach")
	e.Git(proj, "branch", "-D", "sub-gone-branch")

	r := e.CLIDirectEnv(proj, e.SessionEnv(sess), "sr-session", "refs", "untrack", "--folder", wt, "--head", "sub-gone-branch", "--reason", "the branch is gone")
	if r.Code != 0 {
		t.Fatalf("untrack of a range whose branch is gone (folder kept): exit %d:\n%s", r.Code, r.Output)
	}
	if r := e.StopNow(proj, sess, false); harness.Blocked(r) && (strings.Contains(r.Output, `sub-gone-branch\" is gone`) || strings.Contains(r.Output, "cannot be read")) {
		t.Fatalf("the untracked range was still refused:\n%s", r.Output)
	}
	r = e.CLIDirectEnv(proj, e.SessionEnv(sess), "sr-session", "refs", "untrack", "--folder", wt, "--head", "never-tracked", "--reason", "x")
	if r.Code == 0 {
		t.Fatalf("an untrack of a head that was never stored succeeded:\n%s", r.Output)
	}
}
