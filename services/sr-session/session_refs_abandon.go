package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// A grounded abandon: the way to say a branch the session committed on is genuinely
// dropped, so Stop stops judging it. Only the user can drop it: the quote must resolve to
// a USER message of this session (the user pool of the citation resolver, never an
// assistant or tool message). The ref is abandoned AT its tip: when the tip moves, or the
// commit is pushed or merged, it is judged again. Deleting a branch is not an abandon: the
// recorded tip is still judged.

func newSessionRefsAbandonCmd() *cobra.Command {
	var ref, folder, quote string
	cmd := &cobra.Command{
		Use:   "abandon --ref <branch> --cite-user '<exact quote>'",
		Short: "Drop a recorded branch the user said to abandon, so Stop no longer judges it",
		Long: `Mark a recorded ref abandoned so it is no longer judged at Stop.

Accepted only when --cite-user is an exact quote of the USER's own words in this session
(the same resolver as ` + "`sr-session trajectory cite`" + `, user pool only): ask the user whether the
branch should be dropped, then cite their answer. An assistant's or a tool's words do not count.

The ref is abandoned AT its current tip. If the tip later moves, or the commit is pushed
(or merged), it is judged again. Deleting the branch does not abandon it.

--folder is the repository's git root (default: the current directory's).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if quote == "" {
				return fmt.Errorf("sloprail: --cite-user is required: the user's own words that drop this branch")
			}
			if !strings.HasPrefix(ref, "refs/") && !strings.HasPrefix(ref, "detached/") {
				ref = "refs/heads/" + ref
			}
			path, _, err := resolveTrajectory(cmd)
			if err != nil {
				return err
			}
			if path == "" {
				return errNoTrajectory()
			}
			matches, err := transcript.CiteInSession(path, quote, []transcript.SourceType{transcript.SourceUser})
			if err != nil {
				return err
			}
			if len(matches) != 1 {
				return fmt.Errorf("sloprail: the quote %q resolves to %d of the user's messages in this session, not exactly one; "+
					"an abandon needs the user's own words (an assistant's or a tool's do not count)", quote, len(matches))
			}
			cwd, err := transcript.StartCwd(path)
			if err != nil || cwd == "" {
				return fmt.Errorf("sloprail: the session record names no starting directory")
			}
			id, err := sessionpath.StableIdentity(path, cwd)
			if err != nil {
				return err
			}
			dbPath, err := sessionDBPath(cwd, id.ID)
			if err != nil {
				return err
			}
			if folder == "" {
				if folder, err = os.Getwd(); err != nil {
					return err
				}
			}
			if root, rerr := gitrepo.Root(folder); rerr == nil && root != "" {
				folder = root
			}
			folder = filepath.Clean(folder)
			store, err := sessionstate.Open(dbPath)
			if err != nil {
				return err
			}
			defer store.Close()
			rows, err := store.Refs(id.ID, folder)
			if err != nil {
				return err
			}
			var row *sessionstate.Ref
			for i := range rows {
				if rows[i].Name == ref {
					row = &rows[i]
				}
			}
			if row == nil {
				return fmt.Errorf("sloprail: %s is not a ref this session recorded in %s (see `sr-session refs list`)", ref, folder)
			}
			tip := row.Tip
			if strings.HasPrefix(ref, "refs/") {
				if cur, err := gitrepo.RefTip(folder, ref); err == nil && cur != "" {
					tip = cur
				}
			}
			if err := store.SetRefAbandoned(id.ID, folder, ref, tip); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "abandoned %s at %s (judged again if its tip moves or it is pushed or merged)\n", ref, tip)
			return nil
		},
	}
	cmd.Flags().StringVar(&ref, "ref", "", "the branch (or detached/<sha12>) to abandon")
	cmd.Flags().StringVar(&folder, "folder", "", "the repository's git root (default: the current directory's)")
	cmd.Flags().StringVar(&quote, "cite-user", "", "an exact quote of the user's own words dropping this branch")
	cmd.Flags().String("path", "", "which trajectory to resolve the quote in; defaults to the current session's")
	_ = cmd.MarkFlagRequired("ref")
	return cmd
}
