package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T015_06: in ONE tree, the dispatching session's state carries across its own
// Stops, and a shared-tree sub-agent's work is judged at the root's Stop only.
//
// A shared-tree dispatch is the case where only the SESSION key can keep two
// sessions' state apart: one cwd, one workspace, one database directory. A
// file-guard is judged per range at the owner's Stop, and a sub-agent sharing the
// tree owns none of it (T015_02), so what can be observed is that the sub-agent's
// own Stop did not run the rule (and so wrote no note into the scope the parent
// reads back) and that the parent's store works across its Stops:
//
//   - the parent's second Stop reads back the note its first Stop stored — the
//     positive control, without which "the sub-agent's note did not appear" holds
//     just as well for a store that never opened;
//   - that note is the parent's own paths, never a note written at the sub-agent's
//     stop, and the sub-agent's file was judged under the parent's identity alone.
//
// Isolated sub-agents' state is asserted in T015_03 and T015_04.
func TestT015_06_InOneTreeTheSubagentIsJudgedAtTheRootsStopWithTheRootsState(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "memo", readsBackItsOwnState, map[string]string{"record.sh": readsBackScript})
	e.GitInit(proj)

	// The sub-agent writes into the SAME tree the root is working in and commits
	// there.
	sub := harness.SubagentScript(t, harness.Turns("sub done",
		harness.CommitFile("sb1", "written-by-the-sub.md", "the sub-agents own work\n", "the sub-agent's work"),
	))

	const sess = "s-015-06"
	e.Run(proj, sess, "work before delegating", Turns("done",
		Bash("rb1", "echo root > root-before.md"),
	).ThenCommit("the root's first work"))
	res := e.Run(proj, sess, "delegate into the same tree", Turns("root done",
		Dispatch("d1", "do it here", sub, ""),
		Bash("rb2", "echo root > root-after.md"),
	).ThenCommit("the root's second work"))

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

	lines := e.FileGuardLedgerLines(proj, "memo", "log")
	if len(lines) == 0 {
		t.Fatalf("nothing was judged at all:\n%s", res.Output)
	}

	// One session judged anything in this tree: the root's. The sub-agent's own
	// Stop ran nothing over a range it does not own.
	ids := map[string]bool{}
	for _, l := range lines {
		ids[sessionOf(l)] = true
	}
	if len(ids) != 1 {
		t.Fatalf("%d sessions judged in a tree the sub-agent shares (%v); want the root's alone. "+
			"Ledger: %v", len(ids), ids, lines)
	}
	if !containsPath(lines, "written-by-the-sub.md") {
		t.Fatalf("the shared-tree sub-agent's committed work was judged by nobody: %v", lines)
	}

	// THE POSITIVE CONTROL: the root's second Stop read back what its first stored.
	after, ok := lineAbout(lines, "root-after.md")
	if !ok {
		t.Fatalf("the root's second Stop judged nothing of its own work (%v)", lines)
	}
	if got := beforeOf(after); !strings.Contains(got, "root-before.md") {
		t.Fatalf("the root's second Stop read back %q, not the note its first Stop stored (which "+
			"names root-before.md) — its store is not working, so the isolation claim above "+
			"would hold for an engine that remembers nothing at all", got)
	}
}
