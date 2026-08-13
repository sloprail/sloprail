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

  sloprail guardrail help    the event kinds this build can produce

There is no setup command. The guardrails directory is created by whatever
writes the first declaration, and a project with none is an ordinary project —
the engine loads cleanly and no session sees a difference.

The session subcommands are invoked by a harness with a payload on stdin, not
typed by a person.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
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
	cmd.AddCommand(
		newSessionStartCmd(), newSessionPreToolCmd(), newSessionStopCmd(),
		newSessionSubagentStopCmd(),
		newSessionStateCmd(), newSessionIDCmd(), newSessionQueryCmd(),
	)
	return cmd
}
