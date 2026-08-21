package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newSessionStartCmd is the hook point that fires when a session begins.
//
// Its one job is to record where this session measures from — the baseline
// commit and branch. It never refuses: a session that cannot start because of a
// guardrail is worse than a session with none.
//
// It does NOT load and report the project's declarations. The new nature
// dispatch reports a malformed declaration on EVERY pre-tool and Stop dispatch
// (newNatureDeclarations → reportNatureInvalid / reportNatureShadowed /
// reportUnresolved), so a fault surfaces the first time the agent does anything —
// rather than only at a session start nobody was watching. Reporting here as
// well would print each fault twice for no gain.
func newSessionStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Session start: record the baseline this session measures from",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := readPayload(cmd)

			// Where this session measures from. The one thing session start is
			// for: without it the first cycle has no point to diff against.
			recordBaseline(cmd, p)
			return nil
		},
	}
}

// recordBaseline records the commit this session measures from and the branch
// it belongs to.
//
// Every failure is reported and swallowed. A session that cannot start because
// of a guardrail is worse than a session with none, and that applies with more
// force to the engine's own bookkeeping than to any rule a project wrote: a
// missing baseline costs the difference this session would have measured, while
// a refusal here costs the session itself. The next cycle asks again, and takes
// the point then if it can.
func recordBaseline(cmd *cobra.Command, p HookPayload) {
	store, err := openEngineState(p)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: no baseline recorded:", err)
		return
	}
	defer store.Close()

	if _, err := ensureBaseline(store, p.Cwd); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: no baseline recorded:", err)
	}
}
