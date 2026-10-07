package transcript

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sloprail/sloprail/internal/harness"
	_ "github.com/sloprail/sloprail/internal/harness/cursor"
)

// Under Cursor the current session is named by CURSOR_TRANSCRIPT_PATH, not by Claude
// Code's CLAUDE_CODE_SESSION_ID; under Claude Code nothing changes.
func TestCurrentSessionPathAsksTheRunningHarness(t *testing.T) {
	t.Setenv(harness.SelectEnv, "cursor")
	t.Setenv("CURSOR_TRANSCRIPT_PATH", "/cursor/agent-transcripts/a/a.jsonl")
	assert.Equal(t, "/cursor/agent-transcripts/a/a.jsonl", CurrentSessionPath(t.TempDir()))

	t.Setenv(harness.SelectEnv, "claudecode")
	assert.Equal(t, "", CurrentSessionPath(t.TempDir()), "Claude Code's lookup does not read Cursor's variables")
}

// A tool a hook started, in a hook whose payload and environment name no session yet
// (Cursor's first tool call of a run), has the engine's answer in SR_TRANSCRIPT; the
// harness's own answer wins when it has one.
func TestCurrentSessionPathFallsBackToTheHooksTranscript(t *testing.T) {
	t.Setenv(harness.SelectEnv, "cursor")
	t.Setenv("CURSOR_TRANSCRIPT_PATH", "")
	t.Setenv("CURSOR_CONVERSATION_ID", "")
	t.Setenv(EnvHookTranscript, "/engine/located.jsonl")
	assert.Equal(t, "/engine/located.jsonl", CurrentSessionPath(t.TempDir()))

	t.Setenv("CURSOR_TRANSCRIPT_PATH", "/cursor/agent-transcripts/a/a.jsonl")
	assert.Equal(t, "/cursor/agent-transcripts/a/a.jsonl", CurrentSessionPath(t.TempDir()))
}
