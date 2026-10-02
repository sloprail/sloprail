package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_60: a project rule added on main mid-session is in force for the session from its
// add commit on. An OLDER branch (cut before the rule, so the rule's folder is absent
// from that branch's history) that the session commits on AFTER the rule was added is
// judged by it, with the rule from the session's rule set; commits made on that branch
// BEFORE the rule existed are not its debt. Skipping the whole branch because the rule
// is absent from its history let any work done there after the rule arrived escape.
func TestT003_60_AProjectRuleAddedOnMainJudgesLaterWorkOnAnOlderBranch(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project, before any rule")
	main := e.Git(proj, "branch", "--show-current")
	const sess = "s-003-60"

	e.Run(proj, sess, "an old branch, before the rule", Turns("done",
		Bash("b1", "git switch -q -c old"),
		harness.CommitFile("c1", "docs/early.md", "FORBIDDEN words", "add early"),
		Bash("b2", "git switch -q "+main),
	))
	time.Sleep(1200 * time.Millisecond) // commit dates have a second's resolution

	led := filepath.Join(t.TempDir(), "ledger.jsonl")
	e.FileGuard(proj, "docs", docsRule, map[string]string{"check.sh": recorder(led)})
	e.CommitAll(proj, "add the rule")
	blocks := stopBlocks(e, proj, sess)

	e.Run(proj, sess, "more work on the old branch", Turns("done",
		Bash("b3", "git switch -q old"),
		harness.CommitFile("c2", "docs/late.md", "FORBIDDEN words", "add late"),
		Bash("b4", "git switch -q "+main),
	))
	got := newBlocks(e, proj, sess, blocks)
	if !strings.Contains(got, refusalText) || !strings.Contains(got, "old") || !strings.Contains(got, "docs/late.md") {
		t.Fatalf("a violation committed on an older branch after the rule was added escaped; refusals:\n%s", got)
	}
	if strings.Contains(got, "docs/early.md") {
		t.Fatalf("a commit made before the rule existed was judged by it:\n%s", got)
	}
	for _, run := range ledger(t, led) {
		if joined := strings.Join(paths(run.Files), " "); strings.Contains(joined, "early.md") {
			t.Fatalf("a file committed before the rule existed was handed to it: %v", paths(run.Files))
		}
	}
	blocks = stopBlocks(e, proj, sess)

	e.Run(proj, sess, "fix it", Turns("fixed",
		Bash("b5", "git switch -q old"),
		harness.CommitFile("c3", "docs/late.md", "clean words", "fix late"),
		Bash("b6", "git switch -q "+main),
	))
	if n := stopBlocks(e, proj, sess); n != blocks {
		t.Fatalf("the fixed branch was still refused:\n%s", newBlocks(e, proj, sess, blocks))
	}
}
