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
		Use:           "sloprail",
		Short:         "Declarative contracts that keep an agent's output honest",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
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
