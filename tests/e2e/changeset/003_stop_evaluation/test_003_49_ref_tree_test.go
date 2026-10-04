package e2e

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"path/filepath"
	"strings"
	"testing"
)

// A judge that reads the project the way a real one does: it passes only when the
// project it was given (sr-agent's --add-dir, which reaches the harness as an argument)
// holds REQUIRED.md. So it answers for WHICH tree it was pointed at.
const requiredFileJudge = `#!/bin/sh
out=""
ok=no
for arg in "$@"; do
  case "$arg" in
    *"Write your answer to the file "*)
      out="$(printf '%s' "$arg" | sed -n 's/.*Write your answer to the file \([^ ]*\)\. .*/\1/p' | head -1)"
      ;;
  esac
  if [ -f "$arg/REQUIRED.md" ]; then ok=yes; fi
done
[ -n "$out" ] || exit 0
if [ "$ok" = yes ]; then
  printf '%s' '{"pass": true, "reasoning": "REQUIRED.md is there"}' > "$out"
else
  printf '%s' '{"pass": false, "reasoning": "JUDGE-SAYS-NO: REQUIRED.md is missing"}' > "$out"
fi
exit 0
`

func requiredProject(t *testing.T) (*Env, string, string) {
	t.Helper()
	e, proj := judgeProject(t, verdictPass)
	e.InstallShim("claude", requiredFileJudge)
	return e, proj, filepath.Join(t.TempDir(), "feat-x-tree")
}

// judgeInTree is the turn an agent takes to have the judges asked about the branch a worktree
// holds: `sr-checks run` from that worktree, over the branch's own commits.
func judgeInTree(wt, base string) harness.Turn {
	return Bash("j-"+filepath.Base(base), "cd "+wt+" && CLAUDECODE=1 CLAUDE_CODE_ENTRYPOINT=cli sr-checks run --base '"+base+"' --head HEAD >/dev/null 2>&1; true")
}

// commitOnX: from the coordinator's own checkout, make a commit on a new branch, then go
// back to main and hand the branch to another worktree (the way a sub-agent holds it).
func commitOnX(withRequired bool, wt string) []harness.Turn {
	cmd := "mkdir -p docs && printf '%s' 'the release is Friday' > docs/a.md"
	if withRequired {
		cmd += " && printf '%s' 'required' > REQUIRED.md"
	}
	return []harness.Turn{
		Bash("b1", "git switch -q -c feat-x"),
		Bash("b2", cmd+" && git add -A && git commit -q -m 'work on x'"),
		Bash("b3", "git switch -q - && git worktree add -q "+wt+" feat-x"),
		judgeInTree(wt, "HEAD~1"),
	}
}

// T003_49: a branch the session committed on, checked out in ANOTHER worktree, is judged
// on that branch's own tree: a judge that needs a file X has and the coordinator's
// checkout lacks is not refused for its absence.
func TestT003_49_ABranchCheckedOutElsewhereIsJudgedOnItsOwnTree(t *testing.T) {
	e, proj, wt := requiredProject(t)

	e.Run(proj, "s-003-49", "work on x", Turns("done", commitOnX(true, wt)...))
	if got := stopRefusals(e, proj, "s-003-49"); got != "" {
		t.Fatalf("the branch was judged against the coordinator's checkout, not its own tree:\n%s", got)
	}
}

// T003_50: the refusal for such a branch names the worktree that holds it, and never
// tells the agent to switch the coordinator's checkout; fixing it THERE passes.
func TestT003_50_TheRefusalNamesTheWorktreeAndNeverSwitchesTheCheckout(t *testing.T) {
	e, proj, wt := requiredProject(t)

	e.Run(proj, "s-003-50", "work on x", Turns("done", commitOnX(false, wt)...))
	got := stopRefusals(e, proj, "s-003-50")
	if !strings.Contains(got, "JUDGE-SAYS-NO") {
		t.Fatalf("the branch lacking the file was not refused:\n%s", got)
	}
	if !strings.Contains(got, filepath.Base(wt)) {
		t.Fatalf("the refusal did not name the worktree holding the branch:\n%s", got)
	}
	if strings.Contains(got, " switch feat-x") || strings.Contains(got, "switch 'feat-x'") {
		t.Fatalf("the refusal told the agent to switch the coordinator's checkout:\n%s", got)
	}
	blocks := stopBlocks(e, proj, "s-003-50")

	e.Run(proj, "s-003-50", "fix it", Turns("fixed",
		// The fix touches the guarded file too: a verdict is cached by the changeset content, so a fix
		// that left docs/a.md as it was would be replayed as the same refusal.
		Bash("b4", "cd "+wt+" && printf '%s' 'required' > REQUIRED.md && printf '%s' ' (confirmed)' >> docs/a.md && git add -A && git commit -q -m 'add the file'"),
		judgeInTree(wt, "HEAD~2"),
	))
	if n := stopBlocks(e, proj, "s-003-50"); n != blocks {
		t.Fatalf("the branch fixed in its own worktree was still refused:\n%s", newBlocks(e, proj, "s-003-50", blocks))
	}
}

// T003_51: a branch somebody else made BEFORE the session, which the coordinator only started a
// rebase onto and moved on from, is not the coordinator's work: its tip never moved during the
// session, so it is not tracked and its judges never run against it here.
func TestT003_51_ARebaseStartedOntoAForeignBranchDoesNotClaimIt(t *testing.T) {
	e, proj, _ := requiredProject(t)
	main := e.Git(proj, "branch", "--show-current")
	e.Git(proj, "switch", "-q", "-c", "feat-x")
	e.WriteFile(proj, "docs/a.md", "the release is Friday\n")
	e.CommitAll(proj, "somebody else's work")
	e.Git(proj, "switch", "-q", main)

	e.Run(proj, "s-003-51", "integrate", Turns("done",
		Bash("b1", "git switch -q -c coordination"),
		Bash("b2", "mkdir -p notes && printf '%s' 'plan' > notes/plan.md && git add -A && git commit -q -m 'plan'"),
		Bash("b3", "git rebase --exec false feat-x >/dev/null 2>&1; git rebase --abort"),
	))
	if got := stopRefusals(e, proj, "s-003-51"); got != "" {
		t.Fatalf("the coordinator answered for a branch it only started a rebase onto:\n%s", got)
	}
	if trackedIn(sessionRanges(t, e, proj, "s-003-51"), proj, "feat-x") {
		t.Fatal("a foreign branch the session never moved is tracked")
	}
}
