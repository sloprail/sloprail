package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
)

// On a harness that cannot give a sub-agent a worktree of its own (no CapWorktrees) the sub-agent
// works in the root's tree. These helpers state what the session then holds: no sub-agent folder,
// every range at the root.

// noSubagentFolder fails when the session registered a worktree folder for a sub-agent: a sub-agent
// sharing the root's tree has none, its work is answered for in the root's folder.
func noSubagentFolder(t *testing.T, e *Env, proj, sess string) {
	t.Helper()
	for _, f := range e.SessionFolders(proj, sess) {
		if f.Role == sessionstate.FolderSubagentWorktree {
			t.Fatalf("a sub-agent sharing the root's tree has a worktree folder of its own: %+v", e.SessionFolders(proj, sess))
		}
	}
}

// trackedHomes is every folder that answers for head with a tracked range.
func trackedHomes(rs []sessionstate.TrackedRange, head string) []string {
	var out []string
	for _, r := range rs {
		if r.Head == head && r.Tracked() {
			out = append(out, realPath(r.Folder))
		}
	}
	return out
}
