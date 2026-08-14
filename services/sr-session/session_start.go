package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module/modules"
)

// newSessionStartCmd is the hook point that fires when a session begins.
//
// It loads the declarations once, so a malformed one surfaces while the person
// is still watching rather than at the moment it would have blocked something.
// It never refuses: a session that cannot start because of a guardrail is worse
// than a session with none.
func newSessionStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Session start: load and report the project's declarations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := readPayload(cmd)

			// Where this session measures from. Recorded before the
			// declarations are touched: a malformed guardrail is a reason to
			// print something, never a reason for the session to have no point
			// to diff against.
			recordBaseline(cmd, p)

			reg, err := modules.Registry()
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
				return nil
			}

			// LoadWith rather than Load: the checks worth running here are the
			// ones that need to know which events exist and what they carry.
			// Session start is where a person is still watching, so it is where
			// a rule that could never fire should say so.
			decls, invalid, err := guardrail.New(dotDir(p.Cwd)).LoadWith(reg)
			if err != nil {
				// Reported, not fatal — see above.
				fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
				return nil
			}
			// The same reporting the pre-tool path does, from the same function.
			// A fault an author reads here and then meets again at a write should
			// be recognisably the one fault, in the one wording.
			reportInvalid(cmd, invalid)

			// Rules that loaded despite something being wrong with the machine.
			// Said differently from "not loaded", because the consequence is
			// different: this rule is in force and will refuse the work it
			// guards until the hook can run.
			for _, d := range decls {
				for _, w := range d.Warnings {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"sloprail: guardrail %q will refuse until this is fixed: %s\n",
						d.Name, w.Message())
				}
			}
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
