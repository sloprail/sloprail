package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_70: KNOWN GAP, not yet fixed (the design is the maintainers' to decide), so it is
// skipped unless SLOPRAIL_KNOWN_GAP_ORPHANED_FOLDERS=1 is set. Each agent judges only its own
// folders. A sub-agent's worktree can vanish (the harness removes agent worktrees, or
// `git worktree remove`) or the agent can finish for good, leaving owed rows under a folder whose
// Stop never comes again: nobody judges them. The merge gate still refuses to merge such a
// tip (T033_03), but no Stop ever refuses it.
//
// The desired behaviour asserted here: a violating commit a sub-agent made on a branch of its
// own worktree, and then lost the worktree of before any Stop could judge it, is still refused
// by some Stop of the session.
func TestT003_70_AnOwedTipUnderARemovedSubagentFolderIsStillJudged(t *testing.T) {
	if os.Getenv("SLOPRAIL_KNOWN_GAP_ORPHANED_FOLDERS") == "" {
		t.Skip("known gap: owed rows under a removed sub-agent folder are never judged; run with SLOPRAIL_KNOWN_GAP_ORPHANED_FOLDERS=1 to see it fail")
	}
	e, proj, _ := project(t, docsRule)
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-a"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "sub adds a"),
		Bash("b2", `wt="$(git rev-parse --show-toplevel)"; cd "$(dirname "$(git rev-parse --git-common-dir)")" && git worktree remove --force "$wt"`),
	))
	res := e.Run(proj, "s-003-70", "delegate", Turns("root done",
		harness.Dispatch("d1", "write the docs", sub, "worktree"),
	))
	refused := e.SubagentStopBlocked(proj, "s-003-70", "") || stopRefusals(e, proj, "s-003-70") != ""
	if !refused {
		t.Fatalf("a violating commit under a removed sub-agent folder was never judged by any Stop:\n%s", res.Output)
	}
}
