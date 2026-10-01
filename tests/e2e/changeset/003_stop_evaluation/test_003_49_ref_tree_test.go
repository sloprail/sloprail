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
	if !strings.Contains(got, "checked out in another worktree") || !strings.Contains(got, filepath.Base(wt)) {
		t.Fatalf("the refusal did not name the worktree holding the branch:\n%s", got)
	}
	if strings.Contains(got, " switch feat-x") || strings.Contains(got, "switch 'feat-x'") {
		t.Fatalf("the refusal told the agent to switch the coordinator's checkout:\n%s", got)
	}
	blocks := stopBlocks(e, proj, "s-003-50")

	e.Run(proj, "s-003-50", "fix it", Turns("fixed",
		Bash("b4", "cd "+wt+" && printf '%s' 'required' > REQUIRED.md && git add -A && git commit -q -m 'add the file'"),
	))
	if n := stopBlocks(e, proj, "s-003-50"); n != blocks {
		t.Fatalf("the branch fixed in its own worktree was still refused:\n%s", newBlocks(e, proj, "s-003-50", blocks))
	}
}

// T003_51: a branch another worktree made, which the coordinator only started a rebase onto
// and moved on from, is not the coordinator's work: it is not claimed, so its judge never
// runs against it here.
func TestT003_51_ARebaseStartedOntoAnotherBranchDoesNotClaimIt(t *testing.T) {
	e, proj, _ := requiredProject(t)

	// The sub-agent's commit and branch are made without touching this folder's HEAD
	// (plumbing stands in for a commit made in another worktree), after the session began.
	subagentCommit := "git branch feat-x && export GIT_INDEX_FILE=$(mktemp -u) && git read-tree HEAD && " +
		"git update-index --add --cacheinfo 100644,$(printf 'the release is Friday' | git hash-object -w --stdin),docs/a.md && " +
		"git update-ref refs/heads/feat-x $(git commit-tree $(git write-tree) -p HEAD -m 'the sub-agent work') && rm -f $GIT_INDEX_FILE"
	// The coordinator starts a rebase onto the sub-agent's branch (HEAD is moved to that
	// branch's tip, "rebase (start)") and backs out of it: nothing was made there.
	e.Run(proj, "s-003-51", "integrate", Turns("done",
		Bash("b0", subagentCommit),
		Bash("b1", "git switch -q -c coordination"),
		Bash("b2", "mkdir -p notes && printf '%s' 'plan' > notes/plan.md && git add -A && git commit -q -m 'plan'"),
		Bash("b3", "git rebase --exec false feat-x >/dev/null 2>&1; git rebase --abort"),
	))
	if got := stopRefusals(e, proj, "s-003-51"); got != "" {
		t.Fatalf("the coordinator answered for a branch it only started a rebase onto:\n%s", got)
	}
}
