package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Under Cursor (named by SLOPRAIL_HARNESS, as the plugin's hook wrapper does), the
// engine reads Cursor's payload and answers in Cursor's shape.

func runPreTool(t *testing.T, stdin string) string {
	t.Helper()
	cmd := newSessionPreToolCmd()
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	require.NoError(t, cmd.RunE(cmd, nil))
	return out.String()
}

// A judge launched under Cursor is held to the grant sr-agent put in its environment,
// whatever the transcript: a write into the readonly project is refused in Cursor's
// deny shape; a write to the answer file is not.
func TestCursorJudgeWriteOutsideItsGrantIsDenied(t *testing.T) {
	t.Setenv("SLOPRAIL_HARNESS", "cursor")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SLOPRAIL_JUDGE_GRANT", `{"writable":["/out"],"readonly":["/proj"]}`)

	denied := runPreTool(t, `{"hook_event_name":"preToolUse","conversation_id":"abc","session_id":"abc","workspace_roots":["/ws"],"tool_name":"Write","tool_input":{"file_path":"/proj/a.go","content":"x"},"transcript_path":null}`)
	assert.JSONEq(t, `{"permission":"deny","user_message":"this judge may read /proj but not change it","agent_message":"this judge may read /proj but not change it"}`, denied)
}

// A Cursor session whose first events carry a null transcript_path is not an
// ephemeral one: the record is where Cursor will write it, so guardrails stay on.
func TestCursorNullTranscriptIsNotAnEphemeralSession(t *testing.T) {
	t.Setenv("SLOPRAIL_HARNESS", "cursor")
	t.Setenv("HOME", t.TempDir())
	in := `{"hook_event_name":"preToolUse","conversation_id":"abc","session_id":"abc","workspace_roots":["/ws/proj"],"tool_name":"Shell","tool_input":{"command":"ls"},"transcript_path":null}`
	p := readPayload(newSessionPreToolCmdWithStdin(in))
	assert.Equal(t, "Bash", p.ToolName)
	assert.False(t, sessionHasNoTranscript(p, false), "the derived, not-yet-written file is an empty transcript")
}

func newSessionPreToolCmdWithStdin(in string) *cobra.Command {
	cmd := newSessionPreToolCmd()
	cmd.SetIn(strings.NewReader(in))
	return cmd
}
