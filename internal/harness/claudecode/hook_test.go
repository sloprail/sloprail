package claudecode

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sloprail/sloprail/internal/harness"
)

func TestParseHook_MapsEveryFieldTheEngineReads(t *testing.T) {
	in := `{"hook_event_name":"PreToolUse","session_id":"s","transcript_path":"/t.jsonl","agent_transcript_path":"/a.jsonl",
"agent_id":"a1","agent_type":"Explore","source":"startup","worktree_path":"/w","cwd":"/c","tool_name":"Write","tool_use_id":"tu",
"tool_input":{"file_path":"x","content":"y"},"stop_hook_active":true,"background_tasks":[{"id":"1"}],"session_crons":[]}`
	got := New().ParseHook(strings.NewReader(in))
	assert.Equal(t, "PreToolUse", got.Event)
	assert.Equal(t, "s", got.SessionID)
	assert.Equal(t, "/t.jsonl", got.TranscriptPath)
	assert.Equal(t, "/a.jsonl", got.AgentTranscriptPath)
	assert.Equal(t, "a1", got.AgentID)
	assert.Equal(t, "Explore", got.AgentType)
	assert.Equal(t, "startup", got.Source)
	assert.Equal(t, "/w", got.WorktreePath)
	assert.Equal(t, "/c", got.Cwd)
	assert.Equal(t, "Write", got.ToolName)
	assert.Equal(t, "tu", got.ToolUseID)
	assert.JSONEq(t, `{"file_path":"x","content":"y"}`, string(got.ToolInput))
	assert.True(t, got.StopHookActive)
	assert.JSONEq(t, `[{"id":"1"}]`, string(got.BackgroundTasks))
	assert.True(t, got.IsSubagent())
}

func TestParseHook_AnEmptyOrUnreadableBodyIsAZeroInput(t *testing.T) {
	assert.Equal(t, harness.HookInput{}, New().ParseHook(strings.NewReader("")))
	assert.Equal(t, harness.HookInput{}, New().ParseHook(strings.NewReader("not json")))
}

func render(t *testing.T, r harness.HookResponse) string {
	t.Helper()
	var b bytes.Buffer
	assert.NoError(t, New().RenderHook(&b, r))
	return b.String()
}

// These strings are what sloprail wrote before the response was neutral, byte for byte.
func TestRenderHook_IsByteIdenticalToWhatClaudeCodeHasAlwaysBeenGiven(t *testing.T) {
	assert.Equal(t,
		`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"no"}}`+"\n",
		render(t, harness.HookResponse{Decision: harness.Deny, Reason: "no"}))
	assert.Equal(t, `{"decision":"block","reason":"keep going"}`+"\n",
		render(t, harness.HookResponse{Decision: harness.Block, Reason: "keep going"}))
	assert.Equal(t, `{"systemMessage":"fyi"}`+"\n", render(t, harness.HookResponse{SystemMessage: "fyi"}))
	assert.Empty(t, render(t, harness.HookResponse{}), "an allow with nothing to say writes nothing")
}

func TestRenderHook_AdditionalContextNamesTheEvent(t *testing.T) {
	assert.Equal(t,
		`{"hookSpecificOutput":{"additionalContext":"ctx","hookEventName":"SessionStart"}}`+"\n",
		render(t, harness.HookResponse{Event: "SessionStart", AdditionalContext: "ctx"}))
}
