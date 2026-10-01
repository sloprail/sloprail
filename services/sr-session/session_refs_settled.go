package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/repochecks"
)

// newSessionRefsSettledCmd answers, for the merge gate and anyone else who asks, whether every
// file-guard of a repository has PASSED a commit: a finished passing run at the commit or at
// a descendant of it, among the check results of the session family (the root's and its
// sub-agents', one database): a tip nobody passed is owed.
func newSessionRefsSettledCmd() *cobra.Command {
	var t refsTarget
	var tip string
	cmd := &cobra.Command{
		Use:   "settled --session <id> --tip <sha>",
		Short: "Has every file-guard passed a commit? Names the rules that have not",
		Long: `Exit 0 when every file-guard declared in the repository has a finished passing run at --tip
or at a commit that contains it, in the session family's check results (the root's and its
sub-agents', one database). Otherwise exit 1 and print the rules that have not, one per
line. A repository with no file-guard has nothing to pass: exit 0.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !objectSha.MatchString(tip) {
				return fmt.Errorf("sloprail: --tip must be a full 40-character commit SHA, got %q", tip)
			}
			ws := t.workspace
			if ws == "" {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				ws = wd
			}
			tree, err := gitrepo.Root(ws)
			if err != nil || tree == "" {
				return fmt.Errorf("sloprail: %s is not inside a git repository", ws)
			}
			mods, err := modules.Registry()
			if err != nil {
				return err
			}
			guards := newNatureDeclarations(cmd, filepath.Clean(tree), mods).FileGuards
			var results checkstore.Store
			if store, err := repochecks.OpenReadOnly(ws, t.session); err == nil {
				results = newFamilyResults(store)
				defer results.Close()
			} else if !errors.Is(err, checkstore.ErrNoStore) {
				return err
			}
			primeGraph(tree, guards, results)
			// A rule that RAN in this session family is one that has to have passed the tip (a
			// declared rule that selected nothing and never ran asks nothing of it); with none having
			// run at all, nothing has judged the work.
			var ran []string
			if results != nil {
				rows, err := results.Query("select distinct check_id from check_runs where check_id like '%file-guard/%' order by check_id")
				if err != nil {
					return err
				}
				for _, r := range rows {
					if id, ok := r["check_id"].(string); ok {
						ran = append(ran, id)
					}
				}
			}
			var missing []string
			if len(guards) > 0 && len(ran) == 0 {
				missing = append(missing, "(no file-guard has judged anything in this session yet)")
			}
			for _, rule := range ran {
				if !judgedByRule(tree, tip, rule, results) {
					missing = append(missing, rule)
				}
			}
			for _, m := range missing {
				fmt.Fprintln(cmd.OutOrStdout(), m)
			}
			if len(missing) > 0 {
				os.Exit(1)
			}
			return nil
		},
	}
	t.flags(cmd)
	cmd.Flags().StringVar(&tip, "tip", "", "the commit (40 hex characters)")
	_ = cmd.MarkFlagRequired("tip")
	return cmd
}

// judgedByRule is judgedByEvery for one rule.
func judgedByRule(root, tip, rule string, results checkstore.Store) bool {
	heads, err := results.PassedHeads(rule)
	if err != nil {
		return false
	}
	ok, _ := gitrepo.AnyDescendant(root, gitrepo.LoadGraph(root), tip, heads)
	return ok
}
