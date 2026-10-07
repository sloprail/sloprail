package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_08: a10n #6 — only an agent that owns the tree is judged. A sub-agent
// working in the session's own tree leaves its commits where the ROOT's Stop
// judges them; refusing the sub-agent for them is how a10n blocked a read-only
// sub-agent 17 times in a row. A sub-agent in a worktree of its own owns that tree
// and is judged on it.
func TestT003_08_ASubagentInTheRootsTreeIsNotJudgedButOneInItsOwnTreeIs(t *testing.T) {
	// Shared tree: the sub-agent commits a file the rule refuses.
	e, proj, led := project(t, docsRule)
	sub := harness.SubagentScript(t, Turns("sub done",
		harness.CommitFile("sb1", "docs/from-sub.md", "FORBIDDEN by the rule", "sub-agent adds a doc"),
	))
	res := e.Run(proj, "s-003-08a", "delegate here", Turns("root done",
		harness.Dispatch("d1", "write the doc", sub, ""),
	))
	if res.AnySubagentStopBlocked() {
		t.Fatalf("a sub-agent in the session's own tree was judged, and refused, for a commit:\n%s", res.Output)
	}
	// The rule did judge the sub-agent's commit — at the root's Stop, again on each
	// retry of it (a script is never replayed).
	if ranAt := ledgerFilePaths(t, led); len(ranAt) == 0 || ranAt[0] != "docs/from-sub.md" {
		t.Fatalf("premise: the rule should have judged the sub-agent's commit at the root's Stop: %v", ranAt)
	}
	if got := e.BlockingErrorsFrom(proj, "s-003-08a", "Stop"); len(got) == 0 {
		t.Fatal("the root, which owns the tree, was not refused for the commit the rule objects to")
	}

	// Own worktree: the sub-agent's commit is judged at ITS Stop.
	e2, proj2, _ := project(t, docsRule)
	sub2 := harness.SubagentScript(t, Turns("sub done",
		harness.CommitFile("sb1", "docs/isolated.md", "FORBIDDEN by the rule", "sub-agent adds a doc in isolation"),
	))
	res = e2.Run(proj2, "s-003-08b", "delegate into isolation", Turns("root done",
		harness.Dispatch("d1", "write the doc", sub2, "worktree"),
	))
	if !res.SubagentStopBlockedWith("FORBIDDEN text in the changeset") {
		t.Fatalf("a sub-agent in its own worktree was not judged on its own commit:\n%s", res.Output)
	}
}

// ledgerFilePaths is every file path the check was handed, across all its runs.
func ledgerFilePaths(t *testing.T, led string) []string {
	t.Helper()
	if _, err := os.Stat(led); err != nil {
		return nil
	}
	var out []string
	for _, r := range ledger(t, led) {
		out = append(out, paths(r.Files)...)
	}
	return out
}
