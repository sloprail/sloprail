// Command sr-session enforces a project's declared guardrails at the moments
// its harness can pause.
//
// It is its own STANDALONE binary, not a subcommand of a single `sloprail`,
// following the shape every high-level sloprail command has: one binary per
// command, with a root `sr` that proxies to them. `sr-mark`, `sr-file` and
// `sr-agent` are the same pattern.
//
//	sr-session start | pre-tool | stop | subagent-stop    the hook points
//	sr-session id | query | state                         what a hook asks
//
// THE SUBCOMMANDS SIT AT THE ROOT, so the hook point is `sr-session start`
// rather than `sr-session session start`. The binary name already carries the
// noun; repeating it would make every hook line say "session" twice. Through
// the root proxy the same command reads `sr session start`, which is where the
// second word comes back — the proxy supplies it as the binary it dispatches
// to, not as an argument this binary parses.
//
// The hook subcommands are invoked by a harness with a payload on stdin, not
// typed by a person. Their exit status IS the verdict, which is why anything
// standing between them and the harness — the root proxy included — has to
// forward that status exactly.
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
		Use:   "sr-session",
		Short: "Session lifecycle — the hook points a harness calls",
		Long: `Enforce a project's declared guardrails at the moments a harness can pause.

A project declares guardrails under ` + DotDirName + `/guardrails/; the harness calls the
hook points below, and the engine runs whichever guardrails bind to what is
about to happen.

  sr-session start          a session is beginning
  sr-session pre-tool       a tool is about to run — the refusable moment
  sr-session stop           a turn has ended
  sr-session subagent-stop  a subagent's turn has ended

  sr-session id | query | state   what a hook asks about the session so far

There is no setup command. The guardrails directory is created by whatever
writes the first declaration, and a project with none is an ordinary project —
the engine loads cleanly and no session sees a difference.

The hook subcommands are invoked by a harness with a payload on stdin, not
typed by a person. For the event kinds a guardrail may bind to, see
` + "`sr-guardrail help`" + `.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newSessionStartCmd(), newSessionPreToolCmd(), newSessionStopCmd(),
		newSessionSubagentStopCmd(),
		newSessionStateCmd(), newSessionIDCmd(), newSessionQueryCmd(),
	)
	return root
}
