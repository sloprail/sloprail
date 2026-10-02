package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/sessionstate"
)

// newSessionWorktreeRemoveCmd is the hook point that fires when the harness removes a worktree
// (a sub-agent finished, the session ended). It never blocks the removal: whatever it cannot do
// it reports on stderr and exits 0. What it does is settle the ranges the session tracked in the
// folder: a branch that still exists in the session's own repository moves there (its commits
// are still verified at Stop), one that is gone moves there pinned at its last tip (never dropped
// unverified). (A harness without this hook is covered the same way at the next hook: a folder
// of the session that no longer exists is settled likewise.)
func newSessionWorktreeRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "worktree-remove",
		Short: "A worktree is being removed: stop tracking the ranges in it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := readPayload(cmd)
			warn := func(format string, a ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: worktree-remove: "+format+"\n", a...)
			}
			if p.WorktreePath == "" {
				warn("the payload names no worktree_path; nothing to untrack")
				return nil
			}
			rs, err := resolveRootSession(p)
			if err != nil {
				warn("the session is not known (%v); nothing to untrack", err)
				return nil
			}
			if _, err := os.Stat(rs.Path); err != nil {
				return nil
			}
			reg, err := sessionstate.Open(rs.Path)
			if err != nil {
				warn("the session's store did not open: %v", err)
				return nil
			}
			defer reg.Close()
			ranges, err := reg.Ranges(rs.ID)
			if err != nil {
				warn("%v", err)
				return nil
			}
			for _, r := range ranges {
				if r.Tracked() && sameDir(r.Folder, p.WorktreePath) {
					dropRemoved(reg, rs.ID, r)
				}
			}
			return nil
		},
	}
}
