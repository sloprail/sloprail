package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_65: a deleted branch's range starts at the merge base with the remote default branch,
// not at the session's start and not at its own cut point: the user's commit that is already
// on origin (a violation, were it in the range) is never reached back to, while the branch's own
// file is refused. The branch is gone at Stop; its range is verified at the commit it pointed at.
func TestT003_65_ADeletedBranchesRangeStartsAtTheRemoteDefaultNotBeforeIt(t *testing.T) {
	e, proj, led := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	const sess = "s-003-65"

	e.Run(proj, sess, "begin", Turns("done", Bash("b0", "true")))
	e.WriteFile(proj, "docs/user.md", "FORBIDDEN words the user committed\n")
	e.CommitAll(proj, "the user's own commit, before the branch")
	e.PushBranch(proj, main) // pushed: origin/main now holds it

	e.Run(proj, sess, "branch, commit, delete", Turns("done",
		Bash("b1", "git switch -q -c side"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("b2", "git switch -q "+main),
		Bash("b3", "git branch -D side"),
	))
	got := stopRefusals(e, proj, sess)
	if !strings.Contains(got, refusalText) || !strings.Contains(got, "docs/a.md") {
		t.Fatalf("the deleted branch's violation was not refused:\n%s", got)
	}
	var judged bool
	for _, run := range ledger(t, led) {
		files := strings.Join(paths(run.Files), " ")
		if strings.Contains(files, "docs/a.md") {
			judged = true
			if strings.Contains(files, "docs/user.md") {
				t.Fatalf("the deleted branch's range reached back past the remote default, to the user's pushed commit: %v", paths(run.Files))
			}
		}
	}
	if !judged {
		t.Fatal("no run was handed the deleted branch's file")
	}
}
