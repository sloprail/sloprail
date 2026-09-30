package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_13: the commit that adds or changes a rule is judged by it. The folder floor
// is the PARENT of the last commit touching the rule's folder; with the commit
// itself as the floor, touching a rule's folder in the same commit as a violation
// would have been a way to get the violation past it.
func TestT003_13_ACommitThatTouchesTheRulesFolderAndViolatesItIsRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.DisableShippedFileGuards(proj)
	e.CommitAll(proj, "the project before the rule")
	e.Run(proj, "s-003-13", "hello", Turns("done", Bash("b1", "true")))

	// Adding: the rule and a violation arrive in one commit.
	led := filepath.Join(t.TempDir(), "ledger.jsonl")
	e.FileGuard(proj, "docs", docsRule, map[string]string{"check.sh": recorder(led)})
	e.WriteFile(proj, "docs/bad.md", "FORBIDDEN words\n")
	e.CommitAll(proj, "the rule, and work that breaks it, together")
	r := e.StopNow(proj, "s-003-13", false)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "FORBIDDEN text in the changeset") {
		t.Fatalf("a commit that adds a rule and violates it was not refused:\n%s", r.Output)
	}

	// Fix it, and let the rule pass: a clean range, and the watermark at its head.
	e.WriteFile(proj, "docs/bad.md", "clean words\n")
	e.CommitAll(proj, "fix the violation")
	if r := e.StopNow(proj, "s-003-13", false); harness.Blocked(r) {
		t.Fatalf("the fixed range was refused:\n%s", r.Output)
	}

	// Changing: the rule's own folder is edited in the same commit as a new violation.
	e.WriteFile(proj, ".sloprail/file-guard/docs/check.sh", recorder(led)+"# a harmless edit\n")
	e.WriteFile(proj, "docs/worse.md", "FORBIDDEN again\n")
	e.CommitAll(proj, "edit the rule, and break it, together")
	r = e.StopNow(proj, "s-003-13", false)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "FORBIDDEN text in the changeset") {
		t.Fatalf("a commit that edits a rule's folder and violates it was not refused:\n%s", r.Output)
	}
	var judged []string
	for _, run := range ledger(t, led) {
		judged = append(judged, paths(run.Files)...)
	}
	if !strings.Contains(strings.Join(judged, " "), "docs/worse.md") {
		t.Fatalf("the violating file was never handed to the rule: %v", judged)
	}
}
