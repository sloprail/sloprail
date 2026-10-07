package cursor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/harness"
)

func env(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }

func TestCurrentTranscriptFromTheShellToolsEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := t.TempDir()
	sub := filepath.Join(proj, "pkg", "deep")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	const id = "c1cba5e7-b85d-487e-8b02-95381492cadb"
	l := New().(harness.CurrentTranscriptLocator)

	// CURSOR_TRANSCRIPT_PATH is taken as it is.
	got, ok := l.CurrentTranscript(env(map[string]string{"CURSOR_TRANSCRIPT_PATH": "/x/y.jsonl", "CURSOR_CONVERSATION_ID": id}), sub)
	assert.True(t, ok)
	assert.Equal(t, "/x/y.jsonl", got)

	// Else derived from the conversation id and the project folder, walking up from where
	// the shell is (its directory persists between calls).
	real, err := filepath.EvalSymlinks(proj)
	require.NoError(t, err)
	want := filepath.Join(New().Transcripts().ProjectDir(filepath.Join(home, ".cursor"), real), "agent-transcripts", id, id+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(want), 0o755))
	require.NoError(t, os.WriteFile(want, []byte("{}\n"), 0o644))
	got, ok = l.CurrentTranscript(env(map[string]string{"CURSOR_CONVERSATION_ID": id}), sub)
	assert.True(t, ok)
	assert.Equal(t, want, got)

	// CURSOR_PROJECT_DIR names the folder directly, from anywhere.
	got, ok = l.CurrentTranscript(env(map[string]string{"CURSOR_CONVERSATION_ID": id, "CURSOR_PROJECT_DIR": proj}), t.TempDir())
	assert.True(t, ok)
	assert.Equal(t, want, got)

	// A Cursor session whose record is not found: handled, and empty, never another harness's answer.
	got, ok = l.CurrentTranscript(env(map[string]string{"CURSOR_CONVERSATION_ID": "no-such"}), sub)
	assert.True(t, ok)
	assert.Empty(t, got)

	// Not a Cursor tool call: no opinion.
	_, ok = l.CurrentTranscript(env(map[string]string{"CLAUDE_CODE_SESSION_ID": "x"}), sub)
	assert.False(t, ok)
}
