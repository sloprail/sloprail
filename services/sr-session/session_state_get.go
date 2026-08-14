package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newSessionStateGetCmd reads back what this guardrail stored.
//
// A key that was never written is empty output and a zero exit, not an error. A
// rule asking whether it has seen something before should not have to tell "no"
// apart from "broken" — and a hook whose shell has errexit set would abort on
// the ordinary first-time case if this failed.
func newSessionStateGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Read back what this guardrail stored under a key",
		Long: `Read back what this guardrail stored under a key.

A key that was never written is empty output and exit 0, not an error. A rule
asking whether it has seen something before should not have to tell "no" apart
from "broken", and a hook running under ` + "`set -e`" + ` would abort on the
ordinary first-time case if this failed.

Which guardrail is asking is never an argument — it comes from ` + GuardrailEnv + `,
which the engine sets when it runs a hook. Outside a hook there is no guardrail
in scope and this says so rather than guessing.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, guardrail, err := openSessionState()
			if err != nil {
				return err
			}
			defer store.Close()

			value, found, err := store.State(guardrail, args[0])
			if err != nil {
				return err
			}
			if !found {
				return nil
			}
			// A trailing newline, so the output reads as a line to a shell and
			// composes with the tools a hook already pipes into.
			fmt.Fprintln(cmd.OutOrStdout(), value)
			return nil
		},
	}
}
