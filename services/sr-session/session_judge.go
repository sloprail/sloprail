package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/module/modules"
)

// newSessionJudgeCmd runs the file-guards over what the agent committed in a repository,
// on demand, exactly as Stop would: HEAD and every other ref the session recorded there.
// It is the engine data a gate needs to judge BEFORE something leaves the machine (the
// shipped `judge-before-push` gate) without the engine knowing about pushes itself.
func newSessionJudgeCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "judge [--dir <repository>]",
		Short: "Run the file-guards over what the session committed in a repository, as Stop would",
		Long: `Run every file-guard over what this session committed in a repository: HEAD and each
other branch it recorded there (` + "`sr-session refs list`" + `), under that repository's own rules.
Verdicts are recorded like a Stop's, so the Stop that follows finds a passed range already passed.

Run from a hook script (it reads the session from ` + SessionEnv + ` and ` + TranscriptEnv + `, and the
repository from --dir, else ` + WorkspaceEnv + `, else the current directory).

Exit 0 when nothing refuses. Otherwise the refusals are printed, one block per rule, and the
exit status is 1.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir == "" {
				dir = os.Getenv(WorkspaceEnv)
			}
			if dir == "" || dir == unresolvedWorkspace {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				dir = wd
			}
			tree, err := gitrepo.Root(dir)
			if err != nil || tree == "" {
				return fmt.Errorf("sloprail: %s is not inside a git repository", dir)
			}
			tree = filepath.Clean(tree)
			p := HookPayload{Cwd: tree, TranscriptPath: os.Getenv(TranscriptEnv), SessionID: os.Getenv(SessionEnv)}
			reg, err := modules.Registry()
			if err != nil {
				return err
			}
			scope := natureHookScope(cmd, p)
			adoptOrphans(cmd, p)
			loaded := newNatureDeclarations(cmd, tree, reg)
			if len(loaded.FileGuards) == 0 {
				return nil
			}
			store := natureStore(cmd, p)
			if store != nil {
				defer store.Close()
			}
			results := openChecksStore(cmd, p, scope)
			if results != nil {
				defer results.Close()
			}
			contextMap := loadContextMap(cmd, store, loaded.Contexts)
			var out []string
			for _, r := range evaluateChangesets(cmd, loaded.FileGuards, p, scope, tree, contextMap, store, results) {
				out = append(out, r.Reason+" (file-guard "+r.Attribution+")")
			}
			if len(out) == 0 {
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), strings.Join(out, "\n\n"))
			os.Exit(1)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "a directory inside the repository to judge (default: the session workspace)")
	return cmd
}
