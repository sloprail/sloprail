package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_71: a tip a rule settles without judging a single file does not vanish: the passing run
// is recorded with the reason, and `sr-checks status` (what the agent and the user read) says
// it. A tip whose files were all superseded upstream says "superseded"; a branch cut before a
// rule existed whose commits all predate the rule says "rule-not-in-force".
func TestT003_71_ASettledTipSaysWhyInSrChecksStatus(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	e.Git(proj, "update-ref", "refs/remotes/origin/main", "HEAD")
	const sess = "s-003-71"

	e.Run(proj, sess, "merge it and have it rewritten", Turns("done",
		Bash("b1", "git switch -q -c landed"),
		harness.CommitFile("c1", "docs/one.md", "FORBIDDEN words", "add one"),
		Bash("b2", "git switch -q "+main),
		landUpstream("s1", "landed"),
		rewriteUpstream("s2", "docs/one.md", "clean words"),
	))
	if got := stopRefusals(e, proj, sess); got != "" {
		t.Fatalf("a tip whose only file was superseded upstream was refused:\n%s", got)
	}
	if status := e.ChecksStatus(proj, sess); !strings.Contains(status, "superseded") {
		t.Fatalf("sr-checks status does not say the tip was settled as superseded:\n%s", status)
	}
}

func TestT003_71_ABranchOlderThanTheRuleSaysSoInSrChecksStatus(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project, before any rule")
	main := e.Git(proj, "branch", "--show-current")
	const sess = "s-003-71b"

	e.Run(proj, sess, "an old branch, before the rule", Turns("done",
		Bash("b1", "git switch -q -c old"),
		harness.CommitFile("c1", "docs/early.md", "FORBIDDEN words", "add early"),
		Bash("b2", "git switch -q "+main),
	))
	time.Sleep(1200 * time.Millisecond) // commit dates have a second's resolution
	e.FileGuard(proj, "docs", docsRule, map[string]string{"check.sh": recorder(t.TempDir() + "/ledger.jsonl")})
	e.CommitAll(proj, "add the rule")

	e.Run(proj, sess, "look around", Turns("done", Bash("b3", "true")))
	if got := stopRefusals(e, proj, sess); got != "" {
		t.Fatalf("a commit made before the rule existed was refused:\n%s", got)
	}
	if status := e.ChecksStatus(proj, sess); !strings.Contains(status, "rule-not-in-force") {
		t.Fatalf("sr-checks status does not say the old branch was settled because the rule was not yet in force:\n%s", status)
	}
}
