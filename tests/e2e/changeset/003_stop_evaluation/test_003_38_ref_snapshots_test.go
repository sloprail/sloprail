package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_38: the refs a session touched are recorded from a snapshot at every hook, not
// only read back from the reflog at Stop. Here the reflog is expired before Stop, so
// only what the hooks saw can name the branch the agent left.
func TestT003_38_ASnapshotAtEveryHookKeepsABranchTheReflogForgot(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")

	e.Run(proj, "s-003-38a", "branch, commit, leave, forget", Turns("done",
		Bash("b1", "git switch -q -c feat-a"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("b2", "git switch -q "+main),
		Bash("b3", "git reflog expire --expire=now --all"),
	))
	got := stopRefusals(e, proj, "s-003-38a")
	if !strings.Contains(got, "feat-a") || !strings.Contains(got, refusalText) {
		t.Fatalf("a branch only the hook snapshots saw was not judged:\n%s", got)
	}
	blocks := stopBlocks(e, proj, "s-003-38a")

	e.Run(proj, "s-003-38a", "fix a", Turns("fixed",
		Bash("b4", "git switch -q feat-a"),
		harness.CommitFile("c2", "docs/a.md", "clean words", "fix a"),
		Bash("b5", "git switch -q "+main),
	))
	if n := stopBlocks(e, proj, "s-003-38a"); n != blocks {
		t.Fatalf("the fixed branch was still refused:\n%s", newBlocks(e, proj, "s-003-38a", blocks))
	}
}
