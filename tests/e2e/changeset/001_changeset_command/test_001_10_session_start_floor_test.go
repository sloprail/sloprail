package e2e

import (
	"strings"
	"testing"
)

// startedSession is a repository with docs/a.md committed and a session the mock
// has run in it, so the session's start HEAD is recorded. Returns the recorded
// start commit.
func startedSession(t *testing.T, sessionID string) (e *Env, proj, start string) {
	t.Helper()
	e = New(t)
	proj = e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/a.md", "one\n")
	e.CommitAll(proj, "the project before the session")

	e.Run(proj, sessionID, "hello", Turns("done", Bash("b1", "true")))
	start = e.Meta(proj, sessionID, "baseline_commit")
	if start == "" {
		t.Fatal("the session recorded no start commit")
	}
	// The mock session's own Stop evaluated the rules and recorded runs; a test
	// about the range a rule has not been judged over starts without them.
	e.RemoveCheckResults(proj, sessionID)
	return e, proj, start
}

// T001_10: a repository rule that is not committed yet has no folder floor, so
// the range starts where the session began.
func TestT001_10_UncommittedRuleUsesTheSessionStart(t *testing.T) {
	e, proj, start := startedSession(t, "s-001-10")
	e.FileGuard(proj, "size", docsRule(""), map[string]string{"check.sh": passingCheck})
	e.WriteFile(proj, "docs/a.md", "one\ntwo\n")
	head := e.CommitAllExcept(proj, "an edit during the session", ".sloprail")

	got, res := show(t, e, proj, e.SessionEnv("s-001-10"), "size")
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	if got.Origin != "session-start" || got.Base != start || got.Head != head {
		t.Fatalf("range = %s %s..%s, want session-start %s..%s", got.Origin, got.Base, got.Head, start, head)
	}
	if want := map[string]string{"docs/a.md": "M"}; !equal(filesOf(got), want) {
		t.Fatalf("files = %v, want %v", filesOf(got), want)
	}
}

// T001_11: a plugin's rule lives in the plugin cache, not in this repository, so
// it has no folder floor at all — and other commits in the repo that touch a
// `.sloprail` folder do not stand in for one.
func TestT001_11_PluginRuleUsesTheSessionStart(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/a.md", "one\n")
	e.CommitAll(proj, "the project before the session")
	e.EnablePluginShippingFileGuard(proj, "shipped", "size", docsRule(""), map[string]string{"check.sh": passingCheck})

	e.Run(proj, "s-001-11", "hello", Turns("done", Bash("b1", "true")))
	start := e.Meta(proj, "s-001-11", "baseline_commit")
	e.RemoveCheckResults(proj, "s-001-11")
	e.WriteFile(proj, "docs/a.md", "one\nplugin era\n")
	head := e.CommitAll(proj, "an edit during the session")

	got, res := show(t, e, proj, e.SessionEnv("s-001-11"), "size")
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	if got.Origin != "session-start" || got.Base != start || got.Head != head {
		t.Fatalf("range = %s %s..%s, want session-start %s..%s", got.Origin, got.Base, got.Head, start, head)
	}
	if !strings.Contains(got.Rule, "shipped") {
		t.Fatalf("rule = %q, want the plugin's qualified name", got.Rule)
	}
}

// T001_12: when even the last floor cannot be used the command fails closed. The
// session's start commit is amended away, so it is no longer an ancestor of HEAD;
// the rule has no folder commit either. Committing the rule gives it a floor, but a
// floor alone is not a stand-in for a rewritten start (it can sit after in-session
// commits), so the command still refuses; with a remote branch to anchor on it
// succeeds, from the earlier of the two.
func TestT001_12_AnUnreachableSessionStartFailsClosed(t *testing.T) {
	e, proj, _ := startedSession(t, "s-001-12")
	e.FileGuard(proj, "size", docsRule(""), map[string]string{"check.sh": passingCheck})
	e.Git(proj, "commit", "--amend", "--allow-empty", "-m", "the start commit, rewritten")

	_, res := show(t, e, proj, e.SessionEnv("s-001-12"), "size")
	if res.Code == 0 {
		t.Fatalf("an unreachable session start produced a range:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "not an ancestor") {
		t.Fatalf("the error should say the session start is unreachable:\n%s", res.Output)
	}

	e.CommitAll(proj, "add the rule")
	_, res = show(t, e, proj, e.SessionEnv("s-001-12"), "size")
	if res.Code == 0 || !strings.Contains(res.Output, "can't tell which commits are new") {
		t.Fatalf("a floor alone stood in for a rewritten session start:\n%s", res.Output)
	}

	root := e.Git(proj, "rev-list", "--max-parents=0", "HEAD")
	e.Git(proj, "update-ref", "refs/remotes/origin/main", root)
	got, res := show(t, e, proj, e.SessionEnv("s-001-12"), "size")
	if res.Code != 0 || got.Base != root {
		t.Fatalf("with a remote branch to anchor on: exit %d base %q, want %s:\n%s", res.Code, got.Base, root, res.Output)
	}
}
