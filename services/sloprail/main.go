// Command sloprail enforces a project's declared guardrails at the moments its
// harness can pause.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "sloprail",
		Short: "Declarative contracts that keep an agent's output honest",
		Long: `Declarative contracts that keep an agent's output honest.

A project declares guardrails under ` + DotDirName + `/guardrails/; the harness calls the
session hook points, and the engine runs whichever guardrails bind to what is
about to happen.

  sloprail init              create the directory a project keeps guardrails in
  sloprail guardrail help    how to write one — read this before authoring

The session subcommands are invoked by a harness with a payload on stdin, not
typed by a person.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newInitCmd())
	root.AddCommand(newGuardrailCmd())
	root.AddCommand(newSessionCmd())
	return root
}

// newSessionCmd groups everything that is about one session: the hook points a
// harness calls at the moments it can pause, and the questions a hook asks
// about what the session has done so far.
func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Session lifecycle — the hook points a harness calls",
	}
	cmd.AddCommand(newSessionStartCmd(), newSessionPreToolCmd(), newSessionStopCmd())
	return cmd
}
