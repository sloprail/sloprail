package e2e

import (
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T015_01: an isolated sub-agent's own cycle judges the work IT did, in the tree
// IT was bound to, in a tree the parent does not share.
//
// This is the test 014 says is unwritable. Its note records that an isolated
// sub-agent's tool calls "are never APPLIED", that the bound worktree is
// "EMPTY", and that a test asserting "the sub-agent's own file was judged" was
// written against that and removed rather than weakened.
//
// The measurement behind that was taken with the Write tool, which the mock
// really does not apply. It does apply Bash — and it applies it INSIDE the bound
// worktree, which is the fact that makes the whole scenario reachable. So the
// sub-agent here does its work with Bash, and everything the earlier note ruled
// out follows: there is a tree difference, the worktree's own checkout of
// .sloprail/guardrails makes the project's rule live in it, and the rule fires.
//
// The sub-agent's work is judged where it landed (`sr check run` from inside the
// worktree), and the root's own range never holds that file: the tree is genuinely
// separate, so a root that judged it would be judging another agent's work as its own.
func TestT015_01_AnIsolatedSubagentJudgesItsOwnWorkAsItself(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "recorder", recordsPathAndSession, map[string]string{"record.sh": recordScript})
	e.GitInit(proj)

	// Bash, not Write. The mock executes Bash and applies it in whatever tree the
	// agent is bound to; a Write in a sub-agent's scenario creates no file at
	// all, which is what made this look untestable.
	sub := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "echo 'the sub-agent did this' > only-the-sub-made-this.md"),
	).ThenCommit("the sub-agent's work"))

	res := e.Run(proj, "s-015-01", "delegate into isolation", Turns("root done",
		Bash("rb1", "echo 'the root did this' > only-the-root-made-this.md"),
		Dispatch("d1", "do the delegated job", sub, "worktree"),
	).ThenCommit("the root's work"))

	if !res.Saw("root done") {
		t.Fatalf("the delegated cycle did not complete:\n%s", res.Output)
	}
	if hitRetryCap(res.Output) {
		t.Fatalf("the sub-agent was driven to the retry cap — a trapped cycle, not a judged one:\n%s",
			res.Output)
	}

	wt := theWorktree(t, proj)

	// The sub-agent's work really landed in ITS tree and nowhere else. Without
	// this the rest could be true of a sub-agent that was never isolated.
	if e.Exists(proj, "only-the-sub-made-this.md") {
		t.Fatalf("the sub-agent's file landed in the DISPATCHING session's tree, so the isolation " +
			"was not real and this test is about a shared tree under another name")
	}

	// (1) The sub-agent's own cycle judged the file the sub-agent made.
	e.CheckRunRange(filepath.Join(proj, ".claude", "worktrees", wt), "s-015-01", e.RunBase("s-015-01"), "HEAD")
	subLines := subLedger(t, proj, wt, "recorder", "log")
	if len(subLines) == 0 {
		t.Fatalf("nothing was judged at the isolated sub-agent's own cycle. Its work is in its "+
			"worktree and the project's rule is checked out there with it, so a cycle that judged "+
			"nothing means delegated work went unguarded — the exact hole subagent-stop exists to "+
			"close:\n%s", res.Output)
	}
	if _, ok := lineAbout(subLines, "only-the-sub-made-this.md"); !ok {
		t.Fatalf("the sub-agent's own file was not among what its cycle judged (%v) — a cycle "+
			"that ran but did not see the work it was run for", subLines)
	}

	rootLines := e.FileGuardLedgerLines(proj, "recorder", "log")
	if _, ok := lineAbout(rootLines, "only-the-root-made-this.md"); !ok {
		t.Fatalf("the root's own write was not judged (%v), so the check below would pass vacuously", rootLines)
	}

	// (2) And the reverse error: the root's cycle never saw the sub-agent's file.
	if containsPath(rootLines, "only-the-sub-made-this.md") {
		t.Fatalf("the DISPATCHING session's own cycle judged the sub-agent's file (%v). The two "+
			"are separate trees, so this is the parent being handed another session's work as its "+
			"own", rootLines)
	}
}

// T015_02: a sub-agent SHARING the dispatching session's tree is not judged
// separately — the dispatching session's own Stop judges its committed work.
//
// The shared tree is the ordinary path and the one where the two sessions are
// hardest to keep apart: one directory, one HEAD, one range of commits. A
// file-guard judges commits, and a sub-agent that shares the tree owns nothing of
// it: it commits into the very history the dispatching session's Stop judges, so
// judging the same range at its own stop as well would be one verdict twice, under
// an identity that owns none of the tree. So the sub-agent's own Stop judges
// nothing, and the root's judges its work — once, as itself.
func TestT015_02_ASharedTreeSubagentsWorkIsJudgedAtTheRootsStop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "recorder", recordsPathAndSession, map[string]string{"record.sh": recordScript})
	e.GitInit(proj)

	sub := harness.SubagentScript(t, harness.Turns("sub done",
		harness.CommitFile("sb1", "from-the-sub.md", "delegated\n", "the sub-agent's work"),
	))

	res := e.Run(proj, "s-015-02", "delegate in the same tree", Turns("root done",
		Dispatch("d1", "do it here", sub, ""),
	))

	if !res.Saw("root done") {
		t.Fatalf("the shared-tree delegated cycle did not complete:\n%s", res.Output)
	}
	if hitRetryCap(res.Output) {
		t.Fatalf("the sub-agent hit the retry cap:\n%s", res.Output)
	}

	// The tree really was shared, or this is T015_01 again.
	if trees := worktrees(t, proj); len(trees) != 0 {
		t.Fatalf("a worktree was bound (%v) for a dispatch that asked for none", trees)
	}
	if !e.Exists(proj, "from-the-sub.md") {
		t.Fatalf("the sub-agent's work never reached the shared tree, so nothing here is about a "+
			"cycle judging delegated work:\n%s", res.Output)
	}

	// The root's Stop judges the file the sub-agent committed.
	lines := e.FileGuardLedgerLines(proj, "recorder", "log")
	if !containsPath(lines, "from-the-sub.md") {
		t.Fatalf("the sub-agent's committed file was judged by nobody (%v):\n%s", lines, res.Output)
	}
}

// T015_05: a sub-agent's own cycle judges every file it created, not just one.
//
// A cycle that judged exactly one thing could be an engine judging "the first
// difference it found" or a harness that applies one tool call. Several files in
// one delegated cycle separate "the cycle was judged" from "something fired
// once".
//
// It also pins the multi-turn shape: the sub-agent takes THREE turns, so the
// mock re-runs its script twice and the cycle spans more than a single action.
func TestT015_05_ASubagentsCycleJudgesEverythingItChanged(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "recorder", recordsPathAndSession, map[string]string{"record.sh": recordScript})
	e.GitInit(proj)

	sub := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "echo one > sub-one.md"),
		Bash("sb2", "echo two > sub-two.md"),
		Bash("sb3", "echo three > sub-three.md"),
	).ThenCommit("the sub-agent's work"))

	res := e.Run(proj, "s-015-05", "delegate several turns", Turns("root done",
		Dispatch("d1", "do three things", sub, "worktree"),
	))

	if !res.Saw("root done") {
		t.Fatalf("the multi-turn delegated cycle did not complete:\n%s", res.Output)
	}
	if hitRetryCap(res.Output) {
		t.Fatalf("the sub-agent hit the retry cap:\n%s", res.Output)
	}

	wt := theWorktree(t, proj)
	e.CheckRunRange(filepath.Join(proj, ".claude", "worktrees", wt), "s-015-05", e.RunBase("s-015-05"), "HEAD")
	lines := subLedger(t, proj, wt, "recorder", "log")
	for _, want := range []string{"sub-one.md", "sub-two.md", "sub-three.md"} {
		if !containsPath(lines, want) {
			t.Fatalf("the sub-agent's cycle did not judge %s. A cycle judging only some of what it "+
				"changed leaves the rest unguarded, and a sub-agent taking several turns is the "+
				"ordinary case rather than an unusual one. Ledger: %v", want, lines)
		}
	}
}
