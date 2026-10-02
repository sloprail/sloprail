package e2e

import (
	"encoding/json"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
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
