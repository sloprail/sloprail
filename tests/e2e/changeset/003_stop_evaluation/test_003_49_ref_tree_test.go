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
