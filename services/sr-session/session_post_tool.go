package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/harness"
)

// newSessionPostToolCmd is the hook point after a tool call has run. It decides nothing
// and never answers: its one job is to hand the call's outcome to a harness whose own
// session record does not keep it (harness.ToolResultRecorder: Cursor's transcript has
// no tool_result), so a citation of a tool's output has something to ground against.
func newSessionPostToolCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "post-tool",
		Short: "After a tool call: keep its output where the session record lacks it",
		Args:  cobra.NoArgs,
		RunE:  runSessionPostTool,
	}
}

// runSessionPostTool records the outcome and nothing else. A failure to record is
// reported on stderr and never blocks: the call has already run, and a missing result
// only means a quote of it cannot be grounded.
func runSessionPostTool(cmd *cobra.Command, _ []string) error {
	rec, ok := harness.Current().(harness.ToolResultRecorder)
	if !ok {
		return nil
	}
	p := readPayload(cmd)
	if err := rec.RecordToolResult(p); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: tool result not recorded:", err)
	}
	return nil
}
