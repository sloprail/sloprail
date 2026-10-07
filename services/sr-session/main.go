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
	"runtime/pprof"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/version"
)

func main() {
	// SLOPRAIL_CPUPROFILE=<file> writes a CPU profile of this run (go tool pprof): where a slow hook spends its time.
	if path := os.Getenv("SLOPRAIL_CPUPROFILE"); path != "" {
		if f, err := os.Create(path); err == nil {
			if pprof.StartCPUProfile(f) == nil {
				defer func() { pprof.StopCPUProfile(); f.Close() }()
			}
		}
	}
	if err := newRoot().Execute(); err != nil {
		pprof.StopCPUProfile()
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
  sr-session post-tool      a tool has run (its output is kept where the harness's record lacks it)
  sr-session stop           a turn has ended
  sr-session subagent-start a subagent is starting (recorded in the sub-agent registry)
  sr-session subagent-stop  a subagent's turn has ended
  sr-session worktree-remove  a worktree is being removed (never blocks it)

  sr-session id | query | state   what a hook asks about the session so far
  sr-session refs list|track|untrack   the ranges of commits the session answers for
  sr-session agents list    the sub-agents the session dispatched, and which still run
  sr-session plugins        the plugins the harness resolves for this project (JSON lines)
  sr-session trajectory ...        read a trajectory — describe it, cite into it
  sr-session codex-trust           trust sloprail's plugin hooks in Codex (it skips untrusted ones silently)
  sr-session project-hooks         register the hooks a plugin cannot carry in the project (Cursor: stop, sessionStart)

There is no setup command. The guardrails directory is created by whatever
writes the first declaration, and a project with none is an ordinary project —
the engine loads cleanly and no session sees a difference.

The hook subcommands are invoked by a harness with a payload on stdin, not
typed by a person. The event kinds a guardrail may bind to are per-build, and
` + "`sr-session start`" + ` reports them: a declaration binding to a kind this
build does not produce is refused by name, and the refusal lists every kind it
does produce. To write a guardrail, use the authoring-guardrails skill.`,
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newSessionStartCmd(), newSessionPreToolCmd(), newSessionPostToolCmd(), newSessionStopCmd(),
		newSessionSubagentStartCmd(), newSessionSubagentStopCmd(),
		newSessionStateCmd(), newSessionIDCmd(), newSessionQueryCmd(), newSessionRefsCmd(), newSessionAgentsCmd(), newSessionWorktreeRemoveCmd(),
		newSessionTrajectoryCmd(), newSessionPluginsCmd(), newCodexTrustCmd(), newProjectHooksCmd(), newEmitContextCmd(),
	)
	return root
}
