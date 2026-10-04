// Command sr-test runs a project's end-to-end rule tests. A case lives in its owning rule's folder:
// .sloprail/<nature>/<rule>/tests/<case>/test.sh (or .sloprail/file-guard/structure.tests/<case>/test.sh).
//
// The result subject is "<owner>:<case>" (root .sloprail/) or "<dir>:<owner>:<case>" (nested), and each
// result also carries "owner" ("gate/<rule>", "file-guard/structure").
//
//	sr-test run [path]      run every case (--only <substring of subject>, --rule <nature>/<rule>), one JSONL result each
//	sr-test doctor [path]   list the rules with no case (deterministic; nothing is run)
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
