package e2e

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T003_07: checks read a read-only snapshot of head (SR_TREE), never the working
// tree: an uncommitted edit and an untracked file are invisible to them, they
// cannot write into what they judge, and the snapshot is gone afterwards.
func TestT003_07_ChecksReadAReadOnlySnapshotOfHead(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	out := filepath.Join(t.TempDir(), "seen.txt")
	script := `#!/bin/sh
cat >/dev/null
{
  echo "committed=$(cat "$SR_TREE/docs/a.md")"
  if ( echo tampered > "$SR_TREE/docs/a.md" ) 2>/dev/null; then echo "WROTE-INTO-THE-SNAPSHOT"; fi
  if [ -e "$SR_TREE/scratch.txt" ]; then echo "SCRATCH-VISIBLE"; fi
  echo "tree=$SR_TREE"
} >> ` + out + `
exit 0
`
	e.CommitAll(proj, "the project")
	e.FileGuard(proj, "docs", docsRule, map[string]string{"check.sh": script})
	e.CommitAll(proj, "the rule")
	e.Run(proj, "s-003-07", "hello", Turns("done", Bash("b1", "true")))

	e.WriteFile(proj, "docs/a.md", "committed content\n")
	e.CommitAll(proj, "add a")
	// The working tree moves on after the commit; head does not.
	e.WriteFile(proj, "docs/a.md", "DIRTY working-tree edit\n")
	e.WriteFile(proj, "scratch.txt", "untracked\n")
	// The dirty guarded edit is uncommitted work: committing it is owed first, so
	// put it away for this Stop — what is under test is what the check reads.
	e.Git(proj, "stash", "push", "-q", "-u")
	e.WriteFile(proj, "scratch.txt", "untracked\n")

	r := e.StopJudged(proj, "s-003-07", false)
	if harness.Blocked(r) {
		t.Fatalf("the Stop was refused:\n%s", r.Output)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the check never ran: %v", err)
	}
	seen := string(body)
	if !strings.Contains(seen, "committed=committed content") {
		t.Fatalf("the check did not read the committed content from SR_TREE:\n%s", seen)
	}
	for _, bad := range []string{"WROTE-INTO-THE-SNAPSHOT", "SCRATCH-VISIBLE", "DIRTY"} {
		if strings.Contains(seen, bad) {
			t.Fatalf("the snapshot leaked or was writable (%s):\n%s", bad, seen)
		}
	}
	tree := strings.TrimSpace(seen[strings.Index(seen, "tree=")+len("tree="):])
	if _, err := os.Stat(tree); !os.IsNotExist(err) {
		t.Fatalf("the snapshot %s outlived the Stop", tree)
	}
	if list := e.Git(proj, "worktree", "list"); strings.Count(list, "\n") != 0 {
		t.Fatalf("the snapshot's worktree registration was left behind:\n%s", list)
	}
}
