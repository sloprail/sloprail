// Command sr-test runs a project's end-to-end rule tests. A case lives in its owning rule's folder:
// .sloprail/<nature>/<rule>/tests/<case>/test.sh (or .sloprail/file-guard/structure.tests/<case>/test.sh).
//
// The result subject is "<owner>:<case>" (root .sloprail/) or "<dir>:<owner>:<case>" (nested), and each
// result also carries "owner" ("gate/<rule>", "file-guard/structure").
//
//	sr-test run [path]      run every case (--only <substring of subject>, --rule <nature>/<rule>, --jobs N in parallel, default 5), one JSONL result each
//	sr-test doctor [path]   list the rules with no case (deterministic; nothing is run)
//	sr-test agent <agent.sh>  (inside a test.sh) run the harness mock
//
// Each case runs in a fresh temp dir: its working directory is an empty project (a copy of the
// rules' own .sloprail/ for a project case, nothing for a plugin case) that is already a git
// repository, with the commit identity fixed to "sr-test <sr-test@sloprail.invalid>" through
// GIT_AUTHOR_* / GIT_COMMITTER_*. A test.sh needs no `git init` nor `-c user.name=...`; it may
// still run them, or export its own identity, to override. HOME is a fake empty directory,
// SR_TEST_CASE_DIR is the case's own folder (outside the project), SR_EVENTS_FILE collects the
// events, and SR_CHECKS_JUDGE_MOCKS={} makes an unmocked judge an error (a case never reaches a model).
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
