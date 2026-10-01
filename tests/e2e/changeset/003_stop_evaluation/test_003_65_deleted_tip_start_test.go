package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_65: where an owed tip's range STARTS is data recorded when the ref was first seen, not
// read from the branch's reflog, which goes with the branch when it is deleted. The branch is
// cut from main AFTER a commit of the user's that is itself a violation; the session commits on
// it, deletes it, and Stop judges the tip: only the session's own file is in what that run was
// handed, never the user's commit the branch was cut on top of.
func TestT003_65_ADeletedBranchesRangeStartsWhereItWasCutNotAtTheSessionStart(t *testing.T) {
	e, proj, led := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	const sess = "s-003-65"

	e.Run(proj, sess, "begin", Turns("done", Bash("b0", "true")))
	e.WriteFile(proj, "docs/user.md", "FORBIDDEN words the user committed\n")
	e.CommitAll(proj, "the user's own commit, before the branch")

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
				t.Fatalf("the deleted branch's range reached back past where it was cut, to the user's commit: %v", paths(run.Files))
			}
		}
	}
	if !judged {
		t.Fatal("no run was handed the deleted branch's file")
	}
}
