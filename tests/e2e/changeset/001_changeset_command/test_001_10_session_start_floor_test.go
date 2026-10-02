package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// trackedRanges is `sr-session refs list --json` as the session.
func trackedRanges(t *testing.T, e *Env, proj, sess string) []sessionstate.TrackedRange {
	t.Helper()
	r := e.CLIDirectEnv(proj, e.SessionEnv(sess), "sr-session", "refs", "list", "--json")
	if r.Code != 0 {
		t.Fatalf("refs list: exit %d:\n%s", r.Code, r.Output)
	}
	var out []sessionstate.TrackedRange
	if err := json.Unmarshal([]byte(r.Output), &out); err != nil {
		t.Fatalf("refs list --json is not JSON (%v):\n%s", err, r.Output)
	}
	return out
}

// T001_12: a session's start that the tree left (its commit amended away) does not move the
// range: the folder's tracked range is anchored at the merge base with origin's default branch,
// not at where the session began.
func TestT001_12_ARewrittenSessionStartIsReanchoredAtItsMergeBase(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/a.md", "one\n")
	e.CommitAll(proj, "the project before the session")
	e.Git(proj, "push", "-q", "origin", "main")
	e.Git(proj, "fetch", "-q", "origin")
	mergeBase := e.Git(proj, "rev-parse", "origin/main")

	e.Run(proj, "s-001-12", "hello", Turns("done", Bash("b1", "true")))
	e.Git(proj, "commit", "--amend", "--allow-empty", "-m", "the start commit, rewritten")

	e.Run(proj, "s-001-12", "again", Turns("done", Bash("b2", "true")))
	rs := trackedRanges(t, e, proj, "s-001-12")
	if len(rs) != 1 || rs[0].Base != mergeBase || rs[0].Head != "main" {
		t.Fatalf("the tracked range after the rewrite = %+v, want main from the merge base with origin/main %s", rs, mergeBase)
	}
}

// T001_10: a repository rule that is not committed yet has no rule-age floor, so the range stays
// the stated one: from the explicit base, not raised to anything.
func TestT001_10_UncommittedRuleHasNoFloor(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/a.md", "one\n")
	start := e.CommitAll(proj, "the project before the work")
	e.FileGuard(proj, "size", docsRule(""), map[string]string{"check.sh": passingCheck})
	e.WriteFile(proj, "docs/a.md", "one\ntwo\n")
	head := e.CommitAllExcept(proj, "an edit", ".sloprail")

	got, res := show(t, e, proj, harness.NoSessionEnv, "size", start)
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	if got.Base != start || got.Head != head {
		t.Fatalf("range = %s..%s, want %s..%s (no floor for an uncommitted rule)", got.Base, got.Head, start, head)
	}
	if len(got.Payload.Changeset.Files) != 1 || got.Payload.Changeset.Files[0].Path != "docs/a.md" {
		t.Fatalf("files = %+v, want docs/a.md", got.Payload.Changeset.Files)
	}
}

// T001_11: a plugin's rule lives in the plugin cache, not in this repository, so it has no
// rule-age floor either: commits that touch other `.sloprail` folders do not stand in for one.
func TestT001_11_PluginRuleHasNoFloor(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/a.md", "one\n")
	start := e.CommitAll(proj, "the project before the work")
	e.EnablePluginShippingFileGuard(proj, "shipped", "size", docsRule(""), map[string]string{"check.sh": passingCheck})
	e.WriteFile(proj, ".sloprail/notes.md", "an unrelated .sloprail commit\n")
	e.CommitAll(proj, "touch .sloprail")
	e.WriteFile(proj, "docs/a.md", "one\nplugin era\n")
	head := e.CommitAll(proj, "an edit")

	got, res := show(t, e, proj, harness.NoSessionEnv, "size", start)
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	if got.Base != start || got.Head != head {
		t.Fatalf("range = %s..%s, want %s..%s (no floor for a plugin rule)", got.Base, got.Head, start, head)
	}
	if !strings.Contains(got.Rule, "shipped") {
		t.Fatalf("rule = %q, want the plugin's qualified name", got.Rule)
	}
}
