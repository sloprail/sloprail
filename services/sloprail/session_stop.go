package main

import "github.com/spf13/cobra"

// newSessionStopCmd is the hook point that fires when a work cycle ends.
//
// What a cycle changed is established here by comparing the tree against where
// the session started, rather than by trusting what any action announced.
func newSessionStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "End of a cycle: run the guardrails bound to what it changed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := readPayload(cmd)
			if p.StopHookActive {
				// Already refused once this cycle. Refusing again would be a
				// loop the agent cannot leave.
				return nil
			}
			return nil // TODO: diff the tree, dispatch the Post events
		},
	}
}
