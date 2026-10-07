package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// T003_43: a root that declares no rules at all still judges the repositories the agent
// worked in outside it, each under its own rules.
func TestT003_43_AnAdHocRepositoryIsJudgedEvenWhenTheRootDeclaresNothing(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "README.md", "root\n")
	e.CommitAll(proj, "a root with no rules")

	other := e.Project()
	e.GitInit(other)
	e.WriteFile(other, "docs/seed.md", "seed\n")
	e.CommitAll(other, "the other project")
	led := filepath.Join(t.TempDir(), "ledger.jsonl")
	e.FileGuard(other, "docs", docsRule, map[string]string{"check.sh": recorder(led)})
	e.CommitAll(other, "the other rule")

	write := func(content string) string {
		return "mkdir -p docs && printf '%s' '" + content + "' > docs/x.md"
	}
	e.Run(proj, "s-003-43", "work elsewhere", Turns("done",
		Bash("b1", "cd "+other+" && "+write("FORBIDDEN words")+" && git add -A && git commit -q -m 'violate'"),
	))
	got := stopRefusals(e, proj, "s-003-43")
	if !strings.Contains(got, refusalText) || !strings.Contains(got, other) {
		t.Fatalf("the other repository's rule was not applied under a rule-less root:\n%s", got)
	}
	blocks := stopBlocks(e, proj, "s-003-43")

	e.Run(proj, "s-003-43", "fix it", Turns("fixed",
		Bash("b2", "cd "+other+" && "+write("clean words")+" && git add -A && git commit -q -m 'fix'"),
	))
	if n := stopBlocks(e, proj, "s-003-43"); n != blocks {
		t.Fatalf("the fixed repository was still refused:\n%s", newBlocks(e, proj, "s-003-43", blocks))
	}
}
