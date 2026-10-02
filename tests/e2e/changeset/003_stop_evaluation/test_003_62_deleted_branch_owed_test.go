package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_62: deleting the branch (or the worktree, or the remote branch) that holds an unjudged
// violating commit does not settle it. The session committed it and no rule passed it, so it
// is still owed a judgement at the next Stop; recreating the branch at its tip and fixing
// there passes.
func TestT003_62_ADeletedBranchWithAnUnjudgedTipIsStillOwed(t *testing.T) {
	cases := []struct {
		name   string
		remote bool
		// script is the Bash that makes the commit and then destroys every ref to it; it
		// leaves the tip's SHA in $SHA_FILE.
		script func(wt, shaFile string) []string
	}{
		{"local branch -D", false, func(wt, sha string) []string {
			return []string{
				"git switch -q -c side",
				"__commit",
				"git rev-parse HEAD > " + sha,
				"git switch -q main",
				"git branch -D side",
			}
		}},
		{"remote branch deleted too", true, func(wt, sha string) []string {
			return []string{
				"git switch -q -c side",
				"__commit",
				"git rev-parse HEAD > " + sha,
				"git push -q origin side",
				"git switch -q main",
				"git branch -D side",
				"git push -q origin --delete side",
				"git update-ref -d refs/remotes/origin/side",
			}
		}},
		{"worktree removed", false, func(wt, sha string) []string {
			return []string{
				"git worktree add -q -b side " + wt,
				"cd " + wt + " && mkdir -p docs && printf '%s' 'FORBIDDEN words' > docs/a.md && git add -A && git commit -q -m 'add a'",
				"git -C " + wt + " rev-parse HEAD > " + sha,
				"git worktree remove --force " + wt,
			}
		}},
		{"worktree removed and branch deleted", false, func(wt, sha string) []string {
			return []string{
				"git worktree add -q -b side " + wt,
				"cd " + wt + " && mkdir -p docs && printf '%s' 'FORBIDDEN words' > docs/a.md && git add -A && git commit -q -m 'add a'",
				"git -C " + wt + " rev-parse HEAD > " + sha,
				"git worktree remove --force " + wt,
				"git branch -D side",
			}
		}},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, proj, _ := project(t, docsRule)
			main := e.Git(proj, "branch", "--show-current")
			if c.remote && e.Origin(proj) == "" {
				bare := filepath.Join(t.TempDir(), "origin.git")
				e.Git(filepath.Dir(bare), "init", "-q", "--bare", bare)
				e.Git(proj, "remote", "add", "origin", bare)
			}
			sess := "s-003-62-" + string(rune('a'+i))
			shaFile := filepath.Join(t.TempDir(), "tip")
			wt := filepath.Join(t.TempDir(), "side-tree")

			var turns []harness.Turn
			for j, cmd := range c.script(wt, shaFile) {
				id := "s" + string(rune('a'+j))
				if cmd == "__commit" {
					turns = append(turns, harness.CommitFile(id, "docs/a.md", "FORBIDDEN words", "add a"))
					continue
				}
				turns = append(turns, Bash(id, cmd))
			}
			e.Run(proj, sess, "commit, then delete everything that holds it", Turns("done", turns...))

			got := stopRefusals(e, proj, sess)
			if !strings.Contains(got, refusalText) || !strings.Contains(got, "docs/a.md") {
				t.Fatalf("a violation whose every ref was deleted escaped judgement; refusals:\n%s", got)
			}
			blocks := stopBlocks(e, proj, sess)

			e.Run(proj, sess, "bring it back and fix it", Turns("fixed",
				Bash("f1", "git switch -q -C side $(cat "+shaFile+")"),
				harness.CommitFile("f2", "docs/a.md", "clean words", "fix a"),
				Bash("f3", "git switch -q "+main),
			))
			if n := stopBlocks(e, proj, sess); n != blocks {
				t.Fatalf("the restored and fixed branch was still refused:\n%s", newBlocks(e, proj, sess, blocks))
			}
		})
	}
}
