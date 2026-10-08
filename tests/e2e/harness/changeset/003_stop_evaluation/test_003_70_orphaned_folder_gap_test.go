package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_70: a sub-agent commits a violation on a branch of its own worktree, and the worktree
// folder is removed before any Stop judged it. A tracked folder that no longer exists gets no
// Stop of its own; what it still owes goes back to the parent, whose Stop judges it from the
// branch (or its pinned tip), which survive the folder. Under both settings of
// enable_subagent_stop_check: the sub-agent's own Stop cannot run in a folder that is gone, so
// the parent's refuses either way.
//
// On a harness with no worktree of its own for a sub-agent (no CapWorktrees) the sub-agent works
// in the root's tree: there is no sub-agent folder to lose, the range was the root's from the
// start, and the root's Stop refuses it.
func TestT003_70_AnOwedTipUnderARemovedSubagentFolderIsStillJudged(t *testing.T) {
	for _, tc := range []struct {
		name         string
		subagentStop bool
	}{
		{"subagent stop check on", true},
		{"subagent stop check off", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, proj, _ := project(t, docsRule)
			if !tc.subagentStop {
				cfg := filepath.Join(proj, ".sloprail", "config.yaml")
				body, err := os.ReadFile(cfg)
				if err != nil {
					t.Fatal(err)
				}
				e.WriteFile(proj, ".sloprail/config.yaml", strings.Replace(string(body), "enable_subagent_stop_check: true", "enable_subagent_stop_check: false", 1))
				e.CommitAll(proj, "sub-agent stop check off")
			}
			const sess = "s-003-70"
			steps := []harness.Turn{
				Bash("b1", "git switch -q -c sub-a"),
				harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "sub adds a"),
			}
			hasWT := harness.HasCap(t, harness.CapWorktrees)
			if hasWT {
				steps = append(steps, Bash("b2", `wt="$(git rev-parse --show-toplevel)"; cd "$(dirname "$(git rev-parse --git-common-dir)")" && git worktree remove --force "$wt"`))
			}
			sub := harness.SubagentScript(t, Turns("sub done", steps...))
			res := e.Run(proj, sess, "delegate", Turns("root done",
				harness.Dispatch("d1", "write the docs", sub, harness.OwnTree(t)),
			))
			if hasWT {
				// Premise: no sub-agent worktree folder is left on disk.
				if left, _ := os.ReadDir(filepath.Join(proj, ".claude", "worktrees")); len(left) != 0 {
					t.Fatalf("premise: the sub-agent's worktree folder still exists: %v", left)
				}
			} else {
				noSubagentFolder(t, e, proj, sess)
			}
			if !stopRefusalsMention(e, proj, sess, "sub-a") {
				t.Fatalf("a violating commit of a sub-agent whose folder is gone (worktrees: %v) was never refused by the parent's Stop:\n%s", hasWT, res.Output)
			}
		})
	}
}

// stopRefusalsMention reports whether a Stop of the root session refused naming the branch.
func stopRefusalsMention(e *Env, proj, sess, branch string) bool {
	return strings.Contains(stopRefusals(e, proj, sess), branch)
}
