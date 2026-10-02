package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T035_09: the gate ships OFF. In a project that never turned it on, `git branch -D` of a
// branch holding commits a rule refused goes through.
func TestT035_09_TheGateIsOffByDefault(t *testing.T) {
	const sess = "s-035-09"
	e := harness.New(t, harness.WithoutShippedFileGuards())
	e.SetStopBlockCap(1)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project")
	e.FileGuard(proj, "docs", ruleYAML, map[string]string{"check.sh": checkSh})
	e.CommitAll(proj, "the rule")

	e.Run(proj, sess, "break it", Turns("done", leaveBranch("bad", "FORBIDDEN words")...))
	res := e.Run(proj, sess, "drop it", Turns("done", Bash("d1", "git branch -D bad")))
	if res.Saw("no-destroying-owed-work") {
		t.Fatalf("the destroying gate fired although it ships off:\n%s", res.Output)
	}
	if e.Git(proj, "branch", "--list", "bad") != "" {
		t.Fatalf("the delete did not go through")
	}
}
