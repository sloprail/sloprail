package e2e

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"strings"
	"testing"
)

// T001_02: a range that is empty is an answer; a range that could not be
// computed is an error, and the two are never the same thing.
//
// The failing half first: a command with no --base has no range, so it refuses instead of
// guessing. Given the base, the range — with nothing after the rule's commit — selects
// no files, which is a success.
// sr:proves fileguard/nothing-selected-passes
func TestT001_02_EmptyRangeIsAnAnswerAndNoBaseIsAnError(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/a.md", "one\n")
	before := e.CommitAll(proj, "before")
	e.FileGuard(proj, "size", docsRule(""), map[string]string{"check.sh": passingCheck})

	_, res := show(t, e, proj, harness.NoSessionEnv, "size", "")
	if res.Code == 0 {
		t.Fatalf("a changeset with no base was given a range:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "base") {
		t.Fatalf("the error should say what was missing (the base):\n%s", res.Output)
	}

	head := e.CommitAll(proj, "add the rule")
	got, res := show(t, e, proj, harness.NoSessionEnv, "size", before)
	if res.Code != 0 {
		t.Fatalf("after committing the rule: exit %d:\n%s", res.Code, res.Output)
	}
	// The base is the commit before the rule's, so that commit is in the range —
	// and, selecting nothing, it is a pass with no files.
	if got.Base != before || got.Head != head {
		t.Fatalf("range = %s..%s, want %s..%s", got.Base, got.Head, before, head)
	}
	if len(got.Payload.Changeset.Files) != 0 || len(got.Payload.Changeset.Commits) != 1 {
		t.Fatalf("a range where match selects nothing holds no files: %+v", got.Payload.Changeset)
	}
}

// T001_03: a git error is an error, never an empty changeset.
// sr:proves fileguard/unreadable-range-refuses
func TestT001_03_AGitErrorFailsClosed(t *testing.T) {
	e, proj, floor, _ := repoWithRule(t, docsRule(""))
	e.WriteFile(proj, ".git/HEAD", "garbage\n")

	_, res := show(t, e, proj, harness.NoSessionEnv, "size", floor)
	if res.Code == 0 {
		t.Fatalf("a repository git cannot read produced a changeset:\n%s", res.Output)
	}
}

// T001_04: a --rule that names nothing loaded is an error that says what is loaded.
func TestT001_04_UnknownRuleIsAnError(t *testing.T) {
	e, proj, floor, _ := repoWithRule(t, docsRule(""))
	_, res := show(t, e, proj, harness.NoSessionEnv, "no-such-rule", floor)
	if res.Code == 0 || !strings.Contains(res.Output, "file-guard/size") {
		t.Fatalf("exit %d, want a refusal naming the loaded rules:\n%s", res.Code, res.Output)
	}
	if _, res := show(t, e, proj, harness.NoSessionEnv, "file-guard/size", floor); res.Code != 0 {
		t.Fatalf("the qualified name should select the rule: exit %d\n%s", res.Code, res.Output)
	}
}
