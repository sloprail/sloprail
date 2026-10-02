package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/gitrepo"
)

func newDefaultBaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "default-base --head <rev>",
		Short: "Print where work on head started: its merge base with the default branch",
		Long: `Print the sha a range over --head starts at when nobody stated one: the merge base of --head with
the REMOTE default branch (origin's HEAD, else origin/main, origin/master). Prints git's empty tree
(4b825dc642cb6eb9a060e54bf8d69288fbee4904) when --head shares no history with it: everything is judged.
Exits non-zero when there is no remote default branch (pass --base instead). Read-only.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			headRev, _ := cmd.Flags().GetString("head")
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("sloprail: working directory: %w", err)
			}
			root, err := gitrepo.Root(cwd)
			if err != nil || root == "" {
				return fmt.Errorf("sloprail: %s is not inside a git repository", cwd)
			}
			root = filepath.Clean(root)
			r, err := gitrepo.ResolveRange(root, headRev, headRev)
			if err != nil {
				return fmt.Errorf("sloprail: %w", err)
			}
			base, ok := gitrepo.DefaultBase(root, r.Head)
			if !ok {
				return fmt.Errorf("sloprail: no remote default branch to start %s from; pass --base", headRev)
			}
			fmt.Fprintln(cmd.OutOrStdout(), base)
			return nil
		},
	}
	cmd.Flags().String("head", "", "The head revision: a branch, tag or sha (required)")
	_ = cmd.MarkFlagRequired("head")
	return cmd
}
