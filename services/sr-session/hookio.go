package main

import (
	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/harness/claudecode"
)

// HookPayload is what the Claude Code harness puts on a hook's standard input; the
// type and its record resolution live in internal/harness/claudecode.
type HookPayload = claudecode.HookPayload

// readPayload reads the hook payload from stdin.
func readPayload(cmd *cobra.Command) HookPayload {
	return claudecode.ReadHook(cmd.InOrStdin())
}

// deny refuses a pending tool call, in the shape the harness expects.
func deny(cmd *cobra.Command, reason string) error {
	return claudecode.DenyOutput(cmd.OutOrStdout(), reason)
}

// block refuses to let a cycle end, in the shape the harness expects.
func block(cmd *cobra.Command, reason string) error {
	return claudecode.BlockOutput(cmd.OutOrStdout(), reason)
}
