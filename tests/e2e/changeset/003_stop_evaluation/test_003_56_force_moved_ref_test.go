package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

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
