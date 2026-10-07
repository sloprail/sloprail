package main

import (
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/harness"
)

// newEmitContextCmd writes the text on stdin as the SessionStart context of the
// running harness: the one place that decides what form the agent's start-up text
// takes. The plugin's hook wrapper prints that text (rules-first.md, install
// notices, the load report) as plain lines and pipes it here; harness.RenderHook
// turns it into what the harness injects (Claude Code and Codex: the
// hookSpecificOutput additionalContext document; Cursor: {"additional_context"}).
// Nothing in the wrapper knows which harness it is under.
func newEmitContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "emit-context",
		Short: "Write stdin as the running harness's SessionStart context (used by the plugin hook)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return err
			}
			text := strings.TrimRight(string(raw), "\n")
			if strings.TrimSpace(text) == "" {
				return nil
			}
			return harness.Current().RenderHook(cmd.OutOrStdout(), harness.HookResponse{
				Event:             "SessionStart",
				AdditionalContext: text,
			})
		},
	}
}
