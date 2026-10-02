package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// unjudgedProject is a project with the docs rule and nothing refused yet.
func unjudgedProject(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := harness.New(t, harness.WithoutShippedFileGuards(), harness.WithEnabledShipped("sloprail/gate/no-merge-over-refusals"))
	fakeGH(t, e, nil)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project")
	e.FileGuard(proj, "docs", ruleYAML, map[string]string{"check.sh": checkSh})
	e.CommitAll(proj, "the rule")
	return e, proj
}

// T033_03: the session committed on the branch and no rule has judged its tip yet (the
// Stop has not run): `gh pr merge --admin` is refused, told to end the turn so the Stop
// judges it. Once the Stop has judged and passed the tip, the same merge is not refused.
func TestT033_03_AnUnjudgedTipIsRefusedThenJudgedThenAllowed(t *testing.T) {
	const sess = "s-033-03"
	e, proj := unjudgedProject(t)

	res := e.Run(proj, sess, "write and merge the docs", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "clean words", "add a"),
		Bash("m1", "gh pr merge --admin --squash"),
	))
	if !res.Refused() || !res.Saw("no-merge-over-refusals") || !res.Saw("end your turn") {
		t.Fatalf("a merge of a branch no rule had judged was not refused with the way forward:\n%s", res.Output)
	}

	// The turn ended, so the Stop judged the tip and it passed.
	if strings.Contains(e.ChecksStatus(proj, sess, "--failing"), "file-guard/docs") {
		t.Fatalf("premise: the clean commit was refused:\n%s", e.ChecksStatus(proj, sess))
	}
	res = e.Run(proj, sess, "now merge", Turns("done", Bash("m2", "gh pr merge --admin --squash")))
	if res.Saw("no-merge-over-refusals") {
		t.Fatalf("the merge was refused after the tip was judged and passed:\n%s", res.Output)
	}
}
