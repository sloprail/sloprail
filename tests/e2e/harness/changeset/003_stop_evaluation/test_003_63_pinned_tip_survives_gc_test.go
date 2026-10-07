package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_63: a removed worktree's last tip survives git's housekeeping. The sub-agent's branch is
// deleted before its worktree's removal is reported, so the range cannot move to the root under
// the branch's name: it is kept pinned at the last tip (refs/sloprail/pins/…). With the reflog
// expired and the repository garbage-collected --prune=now, nothing but the pin holds the commit,
// and the root's Stop still verifies and refuses it.
func TestT003_63_AGoneBranchsLastTipIsPinnedAndSurvivesGarbageCollection(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	const sess = "s-003-63"
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-a"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "sub adds a"),
	))
	e.Run(proj, sess, "delegate", Turns("root done", harness.Dispatch("d1", "write the docs", sub, "worktree")))
	wt, _ := subagentFolder(t, e, proj, sess)
	tip := strings.TrimSpace(e.Git(proj, "rev-parse", "sub-a"))

	e.Git(proj, "worktree", "remove", "--force", wt)
	e.Git(proj, "branch", "-D", "sub-a")
	removed := worktreeRemoved(t, e, proj, sess, wt)
	if r := removed(); r.Code != 0 {
		t.Fatalf("the hook blocked the removal: exit %d\n%s", r.Code, r.Output)
	}
	if pins := e.Git(proj, "for-each-ref", "refs/sloprail/pins"); !strings.Contains(pins, tip) {
		t.Fatalf("the gone branch's last tip %s was not pinned under refs/sloprail/pins:\n%s", tip, pins)
	}
	e.Git(proj, "reflog", "expire", "--expire=now", "--all")
	e.Git(proj, "gc", "-q", "--prune=now")
	if out := e.Git(proj, "cat-file", "-t", tip); strings.TrimSpace(out) != "commit" {
		t.Fatalf("the pin did not keep the commit through the collection, got %q", out)
	}

	r := e.StopNow(proj, sess, false)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "docs/a.md") || strings.Contains(r.Output, "cannot be read") {
		t.Fatalf("the pinned tip's violation escaped the root's Stop:\n%s", r.Output)
	}
}
