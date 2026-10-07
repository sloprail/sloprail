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
	t.Setenv(SessionIDEnv, "")
	assert.Equal(t, "/cursor/agent-transcripts/a/a.jsonl", CurrentSessionPath(t.TempDir()))

	t.Setenv(harness.SelectEnv, "claudecode")
	assert.Equal(t, "", CurrentSessionPath(t.TempDir()), "Claude Code's lookup does not read Cursor's variables")
}
