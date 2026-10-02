package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/checkrun"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/module/modules"
)

func newStagedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "staged --needs citation [--amend]",
		Short: "The staged files a commit must cite, by the file-guards' `require: citation`",
		Long: `Print, one path per line, the staged files that a file-guard selects and that carry a
` + "`require: citation`" + ` the commit being made must meet.

The candidate change is the index against HEAD (against git's empty tree when HEAD does not exist
yet). With --amend it is the index against HEAD's parent: the amended commit replaces HEAD, so
everything HEAD changed is the commit's again. File-guards are loaded, matched and their
` + "`when`" + ` evaluated exactly as ` + "`sr-checks run`" + ` does over a range, and a requirement whose
` + "`when`" + ` waives a file leaves it out. Nothing is judged, written, staged or committed.

This is the prevention half of the commit-time check: ` + "`run`" + ` and ` + "`verify`" + ` refuse the commit afterwards,
this says before it which files will need a citation. Exits 1 with the reason on stderr when the
change could not be read: no answer is never "nothing is needed".`,
		Args: cobra.NoArgs,
		RunE: runStaged,
	}
	cmd.Flags().String("needs", "", "What to list the staged files for; only 'citation' is known (required)")
	cmd.Flags().Bool("amend", false, "The commit replaces HEAD: judge the index against HEAD's parent")
	_ = cmd.MarkFlagRequired("needs")
	return cmd
}

func runStaged(cmd *cobra.Command, _ []string) error {
	if needs, _ := cmd.Flags().GetString("needs"); needs != "citation" {
		return fmt.Errorf("sloprail: --needs %q is not known; the only value is 'citation'", needs)
	}
	amend, _ := cmd.Flags().GetBool("amend")
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("sloprail: working directory: %w", err)
	}
	root, err := gitrepo.Root(cwd)
	if err != nil || root == "" {
		return fmt.Errorf("sloprail: %s is not inside a git repository", cwd)
	}
	root = filepath.Clean(root)
	reg, err := modules.Registry()
	if err != nil {
		return err
	}
	// The config at HEAD is what the user committed before this work: it alone may switch off a
	// protected rule (as in resolveTarget). No HEAD, no such config to trust.
	var trusted []string
	if gitrepo.HasCommits(root) {
		trusted = append(trusted, "HEAD")
	}
	loaded := checkrun.LoadDeclarations(cmd.ErrOrStderr(), root, reg, trusted...)
	sess := openSession(root)
	defer sess.close()
	paths, err := checkrun.StagedNeedingCitation(checkrun.StagedParams{
		Err: cmd.ErrOrStderr(), Guards: loaded.FileGuards, Root: root, Amend: amend,
		ContextMap: checkrun.LoadContextMap(cmd.ErrOrStderr(), sess.state, loaded.Contexts),
	})
	if err != nil {
		return fmt.Errorf("sloprail: %w", err)
	}
	for _, p := range paths {
		fmt.Fprintln(cmd.OutOrStdout(), p)
	}
	return nil
}
