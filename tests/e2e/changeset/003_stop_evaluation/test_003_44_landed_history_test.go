package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_44: a branch squash-merged long ago still counts as landed after upstream edited
// the same file again (its version is in upstream's history of the path); a branch never
// merged is judged.
func TestT003_44_ALongMergedBranchIsSkippedAndAnUnmergedOneIsJudged(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	e.Git(proj, "update-ref", "refs/remotes/origin/main", "HEAD")

	e.Run(proj, "s-003-44", "merged then edited upstream", Turns("done",
		Bash("b1", "git switch -q -c merged"),
		harness.CommitFile("c1", "docs/m.md", "FORBIDDEN but reviewed", "add m"),
		Bash("b2", "git switch -q "+main),
		squashInto("s1", "merged", main),
		// Upstream edits the same file afterwards: equality with the tip no longer holds.
		Bash("s2", "git switch -q -c tmp-edit refs/remotes/origin/main && printf '%s' 'edited later upstream' > docs/m.md && "+
			"git add -A && git commit -q -m 'edit m' && git update-ref refs/remotes/origin/main HEAD && "+
			"git switch -q "+main+" && git branch -q -D tmp-edit"),
	))
	if got := stopRefusals(e, proj, "s-003-44"); got != "" {
		t.Fatalf("a branch merged long ago was judged again:\n%s", got)
	}
	blocks := stopBlocks(e, proj, "s-003-44")

	e.Run(proj, "s-003-44", "never merged", Turns("done",
		Bash("b3", "git switch -q -c unmerged"),
		harness.CommitFile("c2", "docs/u.md", "FORBIDDEN words", "add u"),
		Bash("b4", "git switch -q "+main),
	))
	got := newBlocks(e, proj, "s-003-44", blocks)
	if !strings.Contains(got, "unmerged") || !strings.Contains(got, refusalText) {
		t.Fatalf("a branch never merged was not judged:\n%s", got)
	}
}
