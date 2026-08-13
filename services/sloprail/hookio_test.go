package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/transcript"
)

func TestPayloadTranscript_PathOnThePayloadWins(t *testing.T) {
	// The harness handing the path over beats any assumption about where it
	// puts things, so it is used verbatim even when an id is also present.
	p := HookPayload{TranscriptPath: "/given/path.jsonl", SessionID: "sid", Cwd: "/proj"}
	got, err := p.record()
	require.NoError(t, err)
	assert.Equal(t, "/given/path.jsonl", got)
}

func TestPayloadTranscript_ReconstructedFromTheSessionID(t *testing.T) {
	// The SessionStart payload carries the session id and the working directory
	// and no path at all — and session start is the one moment the baseline
	// most needs recording. Treating the absent field as "no record" left every
	// session taking its starting point at the END of the first cycle instead,
	// by which time an agent that had committed had already moved it.
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	p := HookPayload{SessionID: "sess-1", Cwd: "/proj"}
	want := filepath.Join(transcript.ProjectDir(cfg, "/proj"), "sess-1.jsonl")
	got, err := p.record()
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestPayloadTranscript_NothingToGoOnIsEmpty(t *testing.T) {
	// No path and no id is genuinely no record, and the caller says so rather
	// than reading a file it invented.
	got, err := HookPayload{Cwd: "/proj"}.record()
	require.NoError(t, err)
	assert.Empty(t, got)
}
