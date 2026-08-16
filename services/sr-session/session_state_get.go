package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newSessionStateGetCmd reads back what this owner stored.
//
// A key that was never written is empty output and a zero exit, not an error. A
// rule asking whether it has seen something before should not have to tell "no"
// apart from "broken" — and a hook whose shell has errexit set would abort on
// the ordinary first-time case if this failed.
func newSessionStateGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Read back what this owner stored under a key",
		Long: `Read back what this owner stored under a key.

A key that was never written is empty output and exit 0, not an error. A rule
asking whether it has seen something before should not have to tell "no" apart
from "broken", and a hook running under ` + "`set -e`" + ` would abort on the
ordinary first-time case if this failed.

Which owner is asking comes from --owner, and is required. Nothing stops a
caller naming another plugin; separate records rest on callers naming themselves
honestly, not on anything enforcing it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ownerFlag, _ := cmd.Flags().GetString("owner")
			store, owner, err := openSessionState(ownerFlag)
			if err != nil {
				return err
			}
			defer store.Close()

			value, found, err := store.State(owner, args[0])
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
