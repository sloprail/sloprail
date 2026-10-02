package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// T003_40: a worktree the agent creates with `git worktree add` becomes an ad-hoc folder,
// and a commit made in it (in the same call or a later one) is judged at Stop, naming
// the folder, while the branch HEAD is on stays clean.
func TestT003_40_ACommitInAWorktreeTheAgentAddedIsJudged(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	wt := filepath.Join(t.TempDir(), "side-tree")

	e.Run(proj, "s-003-40", "side worktree", Turns("done",
		Bash("b1", "git worktree add -q -b side "+wt),
		Bash("b2", "cd "+wt+" && mkdir -p docs && printf '%s' 'FORBIDDEN words' > docs/w.md && git add -A && git commit -q -m 'violate in the worktree'"),
	))
	got := stopRefusals(e, proj, "s-003-40")
	if !strings.Contains(got, refusalText) || !strings.Contains(got, "side-tree") {
		t.Fatalf("a commit in a worktree the agent added was not judged and named:\n%s", got)
	}
	blocks := stopBlocks(e, proj, "s-003-40")

	e.Run(proj, "s-003-40", "fix it", Turns("fixed",
		Bash("b3", "cd "+wt+" && printf '%s' 'clean words' > docs/w.md && git add -A && git commit -q -m 'fix'"),
	))
	if n := stopBlocks(e, proj, "s-003-40"); n != blocks {
		t.Fatalf("the fixed worktree was still refused:\n%s", newBlocks(e, proj, "s-003-40", blocks))
	}
}
