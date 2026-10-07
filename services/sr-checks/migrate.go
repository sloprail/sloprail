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

func newMigrateKeysCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate-keys",
		Short: "Carry the verdicts of an older layout of the check results into the current one, judging nothing",
		Long: `Carry the stored passes of an older layout of the sloprail/checks branch into the current one.

A release that changes what a verdict is keyed by starts a new layout of the results branch. Run
and verify never convert the old one: the new layout starts empty, the older one stays in the
branch untouched, and what was judged before is judged again when it is next run. This command is
the opt-in that spares that: for each rule's subjects it takes the NEWEST stored pass (an older
pass can only be found again if the content goes back to exactly what it was), rebuilds its new
key from the commit range it recorded (the files from git, the rule's subjects script as it is in
the head tree), and files the verdict under it. Nothing is judged and no transcript is read.

It prints progress on stderr and a summary. It is idempotent (a second run does nothing), it never
overwrites a verdict the current layout already holds, and several of them at once are safe: one
migrates, the others wait for it and find it done. Passes whose commits are no longer in the
repository, or whose rule is gone, are skipped and counted, and stay in the older layout.

Needs no range. The rules are the ones the working tree declares.`,
		Args: cobra.NoArgs,
		RunE: runMigrateKeys,
	}
}

func runMigrateKeys(cmd *cobra.Command, _ []string) error {
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
	loaded, err := checkrun.LoadDeclarationsStrict(cmd.ErrOrStderr(), root, reg)
	if err != nil {
		return fmt.Errorf("sloprail: the declarations in this project could not be read: %w", err)
	}
	return checkrun.MigrateKeys(cmd.ErrOrStderr(), root, loaded.FileGuards)
}
