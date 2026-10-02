package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// T003_39: a repository the agent commits in OUTSIDE its own tree is registered as an
// ad-hoc folder before the commit runs, and judged at Stop by THAT repository's rules.
func TestT003_39_ACommitInARepositoryOutsideTheRootIsJudged(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	other := e.Project()
	e.GitInit(other)
	e.WriteFile(other, "docs/seed.md", "seed\n")
	e.CommitAll(other, "the other project")
	otherLed := filepath.Join(t.TempDir(), "other-ledger.jsonl")
	e.FileGuard(other, "docs", docsRule, map[string]string{"check.sh": recorder(otherLed)})
	e.CommitAll(other, "the other rule")

	write := func(content string) string {
		return "mkdir -p docs && printf '%s' '" + content + "' > docs/x.md"
	}
	e.Run(proj, "s-003-39", "work elsewhere", Turns("done",
		Bash("b1", "cd "+other+" && "+write("FORBIDDEN words")+" && git add -A && git commit -q -m 'violate'"),
	))
	got := stopRefusals(e, proj, "s-003-39")
	// A run from another repository finds no session record, so it stores no refusal: the Stop's
	// verify says "not judged yet", naming the folder and the file the rule is about.
	if !strings.Contains(got, "docs/x.md") || !strings.Contains(got, other) {
		t.Fatalf("a commit in a repository outside the root was not judged and named:\n%s", got)
	}
	blocks := stopBlocks(e, proj, "s-003-39")

	e.Run(proj, "s-003-39", "fix it", Turns("fixed",
		Bash("b2", "cd "+other+" && "+write("clean words")+" && git -C "+other+" add -A && git -C "+other+" commit -q -m 'fix'"),
	))
	if n := stopBlocks(e, proj, "s-003-39"); n != blocks {
		t.Fatalf("the fixed ad-hoc repository was still refused:\n%s", newBlocks(e, proj, "s-003-39", blocks))
	}
}
