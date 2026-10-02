package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T015_06: in ONE tree, the dispatching session's state carries across its own calls and
// a shared-tree sub-agent's state is its own.
//
// A shared-tree dispatch is the case where only the SESSION key can keep two sessions'
// state apart: one cwd, one workspace, one database directory. What can be observed is that
// the parent's store works across the dispatch (its second call reads back the note its
// first stored — the positive control, without which "the sub-agent's note did not appear"
// holds just as well for a store that never opened) and that neither scope reads the other's
// note. Isolated sub-agents' state is asserted in T015_03 and T015_04.
func TestT015_06_InOneTreeTheSubagentsStateIsItsOwn(t *testing.T) {
	e := New(t)
	proj := e.Project()
	led := e.NewLedger("memo")
	e.Gate(proj, "memo", memoGate, map[string]string{"record.sh": memoScript(led.Path())})
	e.GitInit(proj)

	// The sub-agent writes into the SAME tree the root is working in.
	sub := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "echo sub > written-by-the-sub.md"),
	).ThenCommit("the sub-agent's work"))

	res := e.Run(proj, "s-015-06", "delegate into the same tree", Turns("root done",
		Bash("rb1", "echo root > root-before.md"),
		Dispatch("d1", "do it here", sub, ""),
		Bash("rb2", "echo root > root-after.md"),
	))
	if !res.Saw("root done") {
		t.Fatalf("the shared-tree delegated cycle did not complete:\n%s", res.Output)
	}
	if hitRetryCap(res.Output) {
		t.Fatalf("the sub-agent hit the retry cap:\n%s", res.Output)
	}
	// One tree, genuinely: no worktree was bound.
	if trees := worktrees(t, proj); len(trees) != 0 {
		t.Fatalf("a worktree was bound (%v) for a dispatch that asked for none. This test is "+
			"about two sessions sharing ONE workspace", trees)
	}

	lines := memoLines(t, led.Path())
	// THE POSITIVE CONTROL: the root's second call read back what its first stored.
	after, ok := lineAbout(lines, "root-after.md")
	if !ok {
		t.Fatalf("the root's second call was never judged (%v)", lines)
	}
	if got := beforeOf(after); !strings.Contains(got, "root-before.md") {
		t.Fatalf("the root's second call read back %q, not the note its first stored (which names "+
			"root-before.md) — its store is not working, so the isolation claim below would hold "+
			"for an engine that remembers nothing at all", got)
	}
	subLine, ok := lineAbout(lines, "written-by-the-sub.md")
	if !ok {
		t.Fatalf("the shared-tree sub-agent's own call was never judged: %v", lines)
	}
	if got := beforeOf(subLine); got != "" {
		t.Fatalf("the shared-tree sub-agent read back %q — the root's note: its scope is pooled "+
			"with the parent's. Ledger: %v", got, lines)
	}
	if strings.Contains(beforeOf(after), "written-by-the-sub.md") {
		t.Fatalf("the root read back the sub-agent's note: %v", lines)
	}
}
