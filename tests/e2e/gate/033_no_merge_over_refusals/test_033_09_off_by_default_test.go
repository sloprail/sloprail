package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T033_09: the gate ships OFF. In a project that never turned it on, `gh pr merge` over a
// branch with an open refusal goes through (the Stop still holds the refusal).
func TestT033_09_TheGateIsOffByDefault(t *testing.T) {
	const sess = "s-033-09"
	e := harness.New(t, harness.WithoutShippedFileGuards())
	fakeGH(t, e, nil)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project")
	e.FileGuard(proj, "docs", ruleYAML, map[string]string{"check.sh": checkSh})
	e.CommitAll(proj, "the rule")
	e.SetStopBlockCap(1)
	e.Run(proj, sess, "write the docs", Turns("done", harness.CommitFile("c1", "docs/a.md", "FORBIDDEN", "add a")))
	if !strings.Contains(e.ChecksStatus(proj, sess, "--failing"), "file-guard/docs") {
		t.Fatalf("premise: the docs rule did not refuse the tip:\n%s", e.ChecksStatus(proj, sess))
	}
	res := e.Run(proj, sess, "merge it", Turns("done", Bash("m1", "gh pr merge --admin --squash")))
	if res.Saw("no-merge-over-refusals") {
		t.Fatalf("the merge gate fired although it ships off:\n%s", res.Output)
	}
}
