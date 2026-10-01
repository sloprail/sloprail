package e2e

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"strings"
	"testing"
)

// T002_06: only an agent that owns the tree is gated. A sub-agent sharing the
// root's tree leaves its work where the root's own Stop will see it, so its
// SubagentStop is not refused for it (a10n blocked a read-only sub-agent 17
// times in a row for the root's uncommitted work) — while the root, who owns
// the tree, still is.
func TestT002_06_ASubagentInTheRootsTreeIsNotGated(t *testing.T) {
	e, proj := project(t)
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("sb1", "echo 'made by the sub-agent' > docs/from-sub.md"),
	))

	res := e.Run(proj, "s-002-06", "delegate here", Turns("root done",
		Dispatch("d1", "write the doc", sub, ""),
	))

	// The sub-agent's own Stop ran (its work is in the tree) and was NOT refused.
	if res.AnySubagentStopBlocked() {
		t.Fatalf("a sub-agent sharing the root's tree was refused for uncommitted work:\n%s", res.Output)
	}
	// Premise: the file really is uncommitted in the shared tree, and the root —
	// who owns it — is refused for it.
	if !strings.Contains(e.Git(proj, "status", "--porcelain"), "docs/from-sub.md") {
		t.Fatal("the sub-agent's file is not in the shared tree, so this proved nothing")
	}
	if errs := harness.CommitRequired(e.BlockingErrorsFrom(proj, "s-002-06", "Stop")); len(errs) == 0 {
		t.Fatal("the root, which owns the tree, was not refused for the sub-agent's uncommitted work")
	}
}

// T002_07: a sub-agent in a worktree of its own owns that tree and is gated on
// it, in the tree it was bound to.
func TestT002_07_AnIsolatedSubagentOwnsItsTreeAndIsGated(t *testing.T) {
	e, proj := project(t)
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("sb1", "echo 'made in isolation' > docs/isolated.md"),
	))

	res := e.Run(proj, "s-002-07", "delegate into isolation", Turns("root done",
		Dispatch("d1", "write the doc", sub, "worktree"),
	))

	// The refusal reaches the sub-agent (the mock prints it as it re-runs it), and
	// names the file in the tree it was bound to.
	if !res.SubagentStopBlocked("Commit your work before ending this turn") || !strings.Contains(res.Output, "docs/isolated.md") {
		t.Fatalf("an isolated sub-agent was not refused for its own uncommitted work:\n%s", res.Output)
	}
}
