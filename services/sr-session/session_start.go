package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/module/modules"
)

// newSessionStartCmd is the hook point that fires when a session begins.
//
// It records where this session measures from — the baseline commit and branch —
// and loads the project's declarations once so a malformed one surfaces while the
// person is still watching. It never refuses: a session that cannot start because
// of a guardrail is worse than a session with none.
//
// The load report is the new-format vocabulary oracle the authoring skill sends
// authors to: `sr-session start` reports a declaration that binds a kind this
// build does not have (naming the kinds it does), a match naming a field the kind
// does not carry (naming the fields it does), a duplicate key, and a check that
// names neither a script nor a judge. newNatureDeclarations produces exactly that
// report (reportNatureInvalid / reportNatureShadowed / reportUnresolved) as a side
// effect of loading — the same report every pre-tool and Stop dispatch prints, so
// a fault an author reads here is recognisably the one they meet at a write.
func newSessionStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Session start: record the baseline and report the project's declarations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := readPayload(cmd)

			// Where this session measures from. Recorded before the declarations
			// are touched: a malformed declaration is a reason to print something,
			// never a reason for the session to have no point to diff against.
			recordBaseline(cmd, p)

			reg, err := modules.Registry()
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
				return nil
			}

			// Load and report. newNatureDeclarations reports every declaration that
			// could not load, every shadowed one, and every unresolved plugin — the
			// load check an author runs, and the one place a person is reliably
			// watching. The loaded set is not used here; session start enforces
			// nothing, by design.
			newNatureDeclarations(cmd, p.Cwd, reg)
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
