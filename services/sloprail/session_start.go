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
			_, invalid, err := guardrail.New(dotDir(p.Cwd)).Load()
			if err != nil {
				// Reported, not fatal — see above.
				fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
				return nil
			}
			for _, iv := range invalid {
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %q not loaded: %s\n", iv.Name, iv.Reason)
			}
			return nil
		},
	}
}
