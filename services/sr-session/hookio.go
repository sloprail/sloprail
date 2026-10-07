package main

import (
	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/hookinput"
)

// HookPayload is what a hook delivers, in the harness-neutral shape; the running
// harness's parser produces it (harness.HookWire).
type HookPayload = harness.HookInput

// readPayload reads the hook payload from stdin.
func readPayload(cmd *cobra.Command) HookPayload {
	return harness.Current().ParseHook(cmd.InOrStdin())
}

// respond writes the engine's answer in the running harness's hook format.
func respond(cmd *cobra.Command, resp harness.HookResponse) error {
	return harness.Current().RenderHook(cmd.OutOrStdout(), resp)
}

// deny refuses a pending tool call, in the shape the harness expects.
func deny(cmd *cobra.Command, reason string) error {
	return respond(cmd, harness.HookResponse{Decision: harness.Deny, Reason: reason})
}

// block refuses to let a cycle end, in the shape the harness expects.
func block(cmd *cobra.Command, reason string) error {
	return respond(cmd, harness.HookResponse{Decision: harness.Block, Reason: reason})
}

// tell shows the person a message without deciding anything.
func tell(cmd *cobra.Command, message string) error {
	return respond(cmd, harness.HookResponse{SystemMessage: message})
}

// recordOf, sessionRecordOf and stateCwdOf resolve a payload's session record and
// the directory its stores are keyed by (internal/hookinput).
func recordOf(p HookPayload) (string, error)        { return hookinput.Record(p) }
func sessionRecordOf(p HookPayload) (string, error) { return hookinput.SessionRecord(p) }
func stateCwdOf(p HookPayload) string               { return hookinput.StateCwd(p) }
