package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionHasNoTranscript(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	proj := t.TempDir()
	const sid = "11111111-2222-3333-4444-555555555555"
	write := func(path string) string {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(`{"sessionId":"`+sid+`","type":"user"}`+"\n"), 0o644))
		return path
	}

	existing := write(filepath.Join(cfg, "projects", "proj-a", sid+".jsonl"))
	missing := filepath.Join(cfg, "projects", "proj-a", "never-written.jsonl")

	// A transcript that exists: the session is on.
	assert.False(t, sessionHasNoTranscript(HookPayload{SessionID: sid, TranscriptPath: existing, Cwd: proj}, false))

	// A path to a file that is never written: off at any hook after SessionStart...
	assert.True(t, sessionHasNoTranscript(HookPayload{SessionID: sid, TranscriptPath: missing, Cwd: proj}, false))
	// ...but not at SessionStart, where a fresh session's file is not written yet.
	assert.False(t, sessionHasNoTranscript(HookPayload{SessionID: sid, TranscriptPath: missing, Cwd: proj, Source: "startup"}, true))

	// An empty transcript_path with a session id at SessionStart is the same: the file may come.
	assert.False(t, sessionHasNoTranscript(HookPayload{SessionID: sid, TranscriptPath: "", Cwd: proj}, true))
	// A later hook with no path and no file under the session id: off.
	assert.True(t, sessionHasNoTranscript(HookPayload{SessionID: sid, TranscriptPath: "", Cwd: proj}, false))

	// A session resumed from a sibling project dir: reported under proj-b, recorded under proj-a.
	// Relocation finds the record, so the session is on.
	reported := filepath.Join(cfg, "projects", "proj-b", sid+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(reported), 0o755))
	assert.False(t, sessionHasNoTranscript(HookPayload{SessionID: sid, TranscriptPath: reported, Cwd: proj, Source: "resume"}, false))

	// No session id (the documented load check, a hand-run command): nothing to switch off.
	assert.False(t, sessionHasNoTranscript(HookPayload{}, false))
	assert.False(t, sessionHasNoTranscript(HookPayload{TranscriptPath: missing}, false))
}

// The notice markers are kept a week and then swept, so they do not pile up.
func TestNoTranscriptMarkersExpire(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache) // macOS: UserCacheDir is under HOME

	assert.True(t, firstNoTranscriptNotice("s-fresh"), "the first hook says it")
	assert.False(t, firstNoTranscriptNotice("s-fresh"), "the second does not")

	dir := filepath.Join(mustCache(t), "sloprail", "no-transcript")
	old := filepath.Join(dir, "s-old")
	require.NoError(t, os.WriteFile(old, nil, 0o644))
	aged := time.Now().Add(-8 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(old, aged, aged))

	assert.True(t, firstNoTranscriptNotice("s-another"))
	_, err := os.Stat(old)
	assert.True(t, os.IsNotExist(err), "a marker older than a week is swept")
	_, err = os.Stat(filepath.Join(dir, "s-fresh"))
	assert.NoError(t, err, "a current one stays")
}

func mustCache(t *testing.T) string {
	t.Helper()
	d, err := os.UserCacheDir()
	require.NoError(t, err)
	return d
}
