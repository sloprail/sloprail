package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newGuardsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guards --base <rev> --head <rev>",
		Short: "Print the name of each file-guard that `run` and `verify` would load, one per line",
		Long: `Print the name of every file-guard this project loads over merge-base(--base, --head)..--head, one per
line: the project's own and every enabled plugin's, minus what the config at the range's base
switches off. The same set run and verify judge. Prints nothing, and exits 0, when there are none.
Read-only; asks no model and writes nothing.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := resolveTarget(cmd)
			if err != nil {
				return err
			}
			defer t.sess.close()
			for _, g := range t.loaded.FileGuards {
				fmt.Fprintln(cmd.OutOrStdout(), g.Name)
			}
			return nil
		},
	}
	addRangeFlags(cmd)
	return cmd
}
