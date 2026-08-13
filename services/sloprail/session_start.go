package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/guardrail"
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

			reg, err := registry()
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
			for _, iv := range invalid {
				// One line per fault, rather than all of them joined. An author
				// reading a terminal is the reason validation reports
				// everything at once, and running them together undoes that.
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %q not loaded:\n", iv.Name)
				for _, reason := range iv.Reasons {
					fmt.Fprintf(cmd.ErrOrStderr(), "  - %s\n", reason)
				}
			}

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
