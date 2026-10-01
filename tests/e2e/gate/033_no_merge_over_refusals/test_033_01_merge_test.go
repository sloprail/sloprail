package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

const ruleYAML = "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n"

const checkSh = `#!/bin/sh
payload="$(cat)"
if printf '%s' "$payload" | jq -e 'any(.changeset.files[]; .newContent | contains("FORBIDDEN"))' >/dev/null; then
  echo '{"reason":"FORBIDDEN text in the docs"}'
  exit 1
fi
exit 0
`

// refusedProject is a project whose docs rule has refused the branch's tip in this
// session: the open refusal the merge gate looks for.
func refusedProject(t *testing.T, sess string) (*harness.Env, string) {
	t.Helper()
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
	return e, proj
}

// T033_01: `gh pr merge` (here with --admin) is refused while the branch has an open
// refusal; once the fix is committed and judged, the same merge is not refused.
func TestT033_01_RefusedThenFixedThenAllowed(t *testing.T) {
	const sess = "s-033-01"
	e, proj := refusedProject(t, sess)

	res := e.Run(proj, sess, "merge it", Turns("done", Bash("m1", "gh pr merge --admin --squash")))
	if !res.Refused() || !res.Saw("file-guard/docs") || !res.Saw("trajectory cite") {
		t.Fatalf("a merge over an open refusal was not refused with the rule and the way past it:\n%s", res.Output)
	}

	e.Run(proj, sess, "fix it", Turns("fixed", harness.CommitFile("c2", "docs/a.md", "clean", "fix a")))
	res = e.Run(proj, sess, "now merge", Turns("done", Bash("m2", "gh pr merge --admin --squash")))
	if res.Saw("no-merge-over-refusals") {
		t.Fatalf("the merge was refused after the refusal was fixed:\n%s", res.Output)
	}
}

// T033_02: the user's own words let the merge through despite the open refusal.
func TestT033_02_AUserCitationAllowsIt(t *testing.T) {
	const sess = "s-033-02"
	e, proj := refusedProject(t, sess)

	const said = "merge it anyway, I accept the refusal"
	res := e.Run(proj, sess, said, Turns("done",
		Bash("m1", "sr-session trajectory cite '"+said+"' && gh pr merge 5 --admin"),
	))
	if res.Saw("no-merge-over-refusals") {
		t.Fatalf("a merge citing the user's words was refused:\n%s", res.Output)
	}
}
