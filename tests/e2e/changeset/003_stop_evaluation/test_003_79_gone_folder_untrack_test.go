package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_79: a sub-agent's worktree folder AND branch are both gone. The Stop's advice, `sr-session
// refs untrack`, must work for that range: the folder is no git repository any more, so the range
// is found by the folder and head it was stored under.
func TestT003_79_AnUntrackNamesARangeWhoseFolderAndBranchAreGone(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	const sess = "s-003-79"
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-gone"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "sub adds a"),
	))
	e.Run(proj, sess, "delegate", Turns("root done", harness.Dispatch("d1", "write the docs", sub, "worktree")))
	wt, _ := subagentFolder(t, e, proj, sess)

	e.Git(proj, "worktree", "remove", "--force", wt)
	e.Git(proj, "branch", "-D", "sub-gone")

	r := e.CLIDirectEnv(proj, e.SessionEnv(sess), "sr-session", "refs", "untrack", "--folder", wt, "--head", "sub-gone", "--reason", "the worktree and its branch are gone")
	if r.Code != 0 {
		t.Fatalf("untrack of a range whose folder is gone: exit %d:\n%s", r.Code, r.Output)
	}
	if r := e.StopNow(proj, sess, false); harness.Blocked(r) && (strings.Contains(r.Output, "sub-gone") || strings.Contains(r.Output, "cannot be read")) {
		t.Fatalf("the untracked range was still refused:\n%s", r.Output)
	}
	r = e.CLIDirectEnv(proj, e.SessionEnv(sess), "sr-session", "refs", "untrack", "--folder", wt, "--head", "never-tracked", "--reason", "x")
	if r.Code == 0 {
		t.Fatalf("an untrack of a range that was never stored succeeded:\n%s", r.Output)
	}
}
