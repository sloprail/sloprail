package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/guardrail"
)

// newInitCmd scaffolds a project's dot-directory.
//
// The one command here a person types rather than a harness invokes, which is
// why it takes its working directory as an argument and prints in prose: the
// hook points read a payload off stdin because nobody is at the keyboard when
// they run, and this one is the opposite case.
func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [dir]",
		Short: "Create a project's .sloprail/ directory",
		Long: `Create the ` + DotDirName + `/ directory a project keeps its guardrails in.

Creates the directory and nothing else. No example guardrail: a rule nobody
chose, sitting in a project as though someone had, is worse than an empty
folder. A project that has adopted sloprail and declared nothing yet is an
ordinary state — the engine loads cleanly and no session sees a difference.

To write the first guardrail, read ` + "`sloprail guardrail help`" + `.

Safe to run twice: it creates what is missing and leaves what is there.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runInit,
	}
}

func runInit(cmd *cobra.Command, args []string) error {
	var cwd string
	if len(args) == 1 {
		cwd = args[0]
	}

	// dotDir is the same resolution every hook point uses, so init creates the
	// directory the engine will look in rather than one that merely resembles
	// it.
	store := guardrail.New(dotDir(cwd))

	created, err := store.Init()
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if !created {
		// Not an error. Re-running setup is how someone checks that setup ran.
		fmt.Fprintf(out, "%s already exists — left as it is.\n", store.GuardrailsDir())
		return nil
	}
	fmt.Fprintf(out, "Created %s\n", store.GuardrailsDir())
	fmt.Fprintf(out, "\nDeclare a guardrail by adding a folder there with a GUARDRAIL.md in it.\n")
	fmt.Fprintf(out, "Run `sloprail guardrail help` for the format.\n")
	return nil
}
