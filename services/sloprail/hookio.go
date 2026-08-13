package main

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"
)

// HookPayload is what a harness puts on a hook's standard input.
//
// A snapshot, not a contract: none of this is published or promised, and every
// field here was established by reading what harnesses actually send. Writing
// it down is what makes a change visible as a difference rather than as a
// guardrail that quietly stops firing.
type HookPayload struct {
	TranscriptPath string          `json:"transcript_path"`
	Cwd            string          `json:"cwd"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	StopHookActive bool            `json:"stop_hook_active"`
}

// Tool implements filemod.Pending: what the harness calls the tool it is about
// to run.
func (p HookPayload) Tool() string { return p.ToolName }

// Arguments implements filemod.Pending: the tool's own arguments, undecoded.
// Kept raw because what they mean is the tool's business and each module reads
// only what it recognises.
func (p HookPayload) Arguments() json.RawMessage { return p.ToolInput }

// readPayload reads the hook payload from stdin.
//
// An unreadable or empty body yields a zero payload rather than an error. A
// hook that cannot read its input has nothing to judge, and an engine that
// failed here would block work for a reason that has nothing to do with any
// rule the project declared.
func readPayload(cmd *cobra.Command) HookPayload {
	var p HookPayload
	b, err := io.ReadAll(cmd.InOrStdin())
	if err != nil || len(b) == 0 {
		return p
	}
	_ = json.Unmarshal(b, &p)
	return p
}

// deny refuses a pending tool call, in the shape this harness expects.
func deny(cmd *cobra.Command, reason string) error {
	return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": reason,
		},
	})
}

// block refuses to let a cycle end, in the shape this harness expects.
func block(cmd *cobra.Command, reason string) error {
	return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
		"decision": "block",
		"reason":   reason,
	})
}
