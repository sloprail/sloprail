package claudecode

import (
	"encoding/json"
	"io"

	"github.com/sloprail/sloprail/internal/harness"
)

// hookPayload is what Claude Code puts on a hook's standard input.
//
// A snapshot, not a contract: none of this is published or promised, and every
// field here was established by reading what the harness actually sends. Writing
// it down is what makes a change visible as a difference rather than as a
// guardrail that quietly stops firing.
type hookPayload struct {
	HookEventName string `json:"hook_event_name"`

	TranscriptPath string `json:"transcript_path"`

	// AgentTranscriptPath is the SUB-AGENT's own record, present when this hook
	// is a sub-agent's rather than the dispatching session's. Claude Code reports
	// it alongside the parent's path rather than instead of it.
	AgentTranscriptPath string `json:"agent_transcript_path"`

	// AgentID names the sub-agent within the session that dispatched it.
	AgentID string `json:"agent_id"`

	// AgentType is the kind of sub-agent, on SubagentStart/SubagentStop.
	AgentType string `json:"agent_type"`

	// SessionID is the id Claude Code currently reports; it re-forks it
	// mid-conversation.
	SessionID string `json:"session_id"`

	// Source is SessionStart's "startup" | "resume" | "clear" | "compact".
	Source string `json:"source"`

	// WorktreePath is the worktree a WorktreeRemove hook reports as being removed.
	WorktreePath string `json:"worktree_path"`

	Cwd            string          `json:"cwd"`
	ToolName       string          `json:"tool_name"`
	ToolUseID      string          `json:"tool_use_id"`
	ToolInput      json.RawMessage `json:"tool_input"`
	StopHookActive bool            `json:"stop_hook_active"`

	// BackgroundTasks and SessionCrons are what a Stop (or SubagentStop) reports
	// still running in the background: Claude Code sends each as a list.
	BackgroundTasks json.RawMessage `json:"background_tasks,omitempty"`
	SessionCrons    json.RawMessage `json:"session_crons,omitempty"`
}

// ParseHook implements harness.HookWire. Claude Code's tool names and argument
// shapes are the canonical vocabulary (harness/tools.go), so they pass through.
func (Harness) ParseHook(r io.Reader) harness.HookInput { return ReadHook(r) }

// ReadHook reads the hook payload from r.
//
// An unreadable or empty body yields a zero input rather than an error: a hook that
// cannot read its input has nothing to judge, and an engine that failed here would
// block work for a reason that has nothing to do with any rule the project declared.
func ReadHook(r io.Reader) harness.HookInput {
	var p hookPayload
	b, err := io.ReadAll(r)
	if err != nil || len(b) == 0 {
		return harness.HookInput{}
	}
	_ = json.Unmarshal(b, &p)
	return harness.HookInput{
		Event:               p.HookEventName,
		SessionID:           p.SessionID,
		Source:              p.Source,
		TranscriptPath:      p.TranscriptPath,
		AgentTranscriptPath: p.AgentTranscriptPath,
		AgentID:             p.AgentID,
		AgentType:           p.AgentType,
		Cwd:                 p.Cwd,
		WorktreePath:        p.WorktreePath,
		ToolName:            p.ToolName,
		ToolUseID:           p.ToolUseID,
		ToolInput:           p.ToolInput,
		StopHookActive:      p.StopHookActive,
		BackgroundTasks:     p.BackgroundTasks,
		SessionCrons:        p.SessionCrons,
	}
}

// RenderHook implements harness.HookWire: Claude Code's hook output.
//
// A Deny is a PreToolUse permissionDecision, a Block is the top-level
// decision:"block" Stop and SubagentStop read, a system message is a top-level
// systemMessage, and additional context is hookSpecificOutput.additionalContext
// under the event's name. An Allow with nothing to say writes nothing.
// sr:invariant gates/refusal-stops-the-action
// sr:invariant gates/stop-refusal-continues-the-turn
func (Harness) RenderHook(w io.Writer, resp harness.HookResponse) error {
	var out map[string]any
	switch resp.Decision {
	case harness.Deny:
		out = map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":            "PreToolUse",
				"permissionDecision":       "deny",
				"permissionDecisionReason": resp.Reason,
			},
		}
	case harness.Block:
		out = map[string]any{"decision": "block", "reason": resp.Reason}
	default:
		out = map[string]any{}
	}
	if resp.SystemMessage != "" {
		out["systemMessage"] = resp.SystemMessage
	}
	if resp.AdditionalContext != "" {
		specific, _ := out["hookSpecificOutput"].(map[string]any)
		if specific == nil {
			specific = map[string]any{"hookEventName": resp.Event}
			out["hookSpecificOutput"] = specific
		}
		specific["additionalContext"] = resp.AdditionalContext
	}
	if len(out) == 0 {
		return nil
	}
	return json.NewEncoder(w).Encode(out)
}
