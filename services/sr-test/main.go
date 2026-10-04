// Command sr-test runs a project's end-to-end rule tests: .sloprail/tests/<case>/test.sh.
//
//	sr-test run [path]      run every case, one JSONL result per case on stdout
//	sr-test doctor [path]   run, then list the rules no case ever exercised
//	sr-test agent <agent.sh>  (inside a test.sh) run the harness mock
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/srtest/agent"
	"github.com/sloprail/sloprail/internal/version"
)

func main() {
	if err := newRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "sr-test <command>",
		Short:         "Run a project's end-to-end rule tests through a harness mock",
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newRunCmd(), newDoctorCmd())
	root.AddCommand(agent.Command())
	return root
}
