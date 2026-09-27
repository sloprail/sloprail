package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeProjectRecord writes a transcript under <configDir>/projects/<dir>/<name>.jsonl.
func writeProjectRecord(t *testing.T, configDir, dir, name string, lines ...string) string {
	t.Helper()
	d := filepath.Join(configDir, "projects", dir)
	require.NoError(t, os.MkdirAll(d, 0o755))
	path := filepath.Join(d, name+".jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	return path
}

// TestDegradedIdentityIsSurfacedOnce: a continuation whose predecessor was
// deleted keeps working under its own root, and the person is told once — not
// on every hook, which is what the identity errors this replaces did.
func TestDegradedIdentityIsSurfacedOnce(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	cwd := t.TempDir()

	path := writeProjectRecord(t, cfg, "-p", "resumed",
		`{"parentUuid":null,"logicalParentUuid":"deleted-long-ago","type":"system","subtype":"compact_boundary","uuid":"continuation-root","timestamp":"2026-07-18T16:34:36.604Z","sessionId":"resumed"}`,
		`{"parentUuid":"continuation-root","type":"assistant","uuid":"a","timestamp":"2026-07-18T16:35:00.000Z","sessionId":"resumed"}`,
	)
	p := HookPayload{TranscriptPath: path, Cwd: cwd}

	id, err := stableIdentity(p)
	require.NoError(t, err, "a deleted predecessor must leave the session an identity, not none")
	assert.Equal(t, "continuation-root", id.ID)
	require.Error(t, id.Degraded)

	var first, second bytes.Buffer
	noteDegradedIdentity(&first, p, id)
	noteDegradedIdentity(&second, p, id)
	assert.Contains(t, first.String(), "continuation-root", "the first hook must say what the session is keyed on")
	assert.Empty(t, second.String(), "every later hook must stay quiet about a condition already reported")

	// And the store opens under it, so state persists across hooks.
	store, err := openEngineState(p)
	require.NoError(t, err)
	require.NoError(t, store.Close())
}

// TestResolvedIdentityIsNotReported: the ordinary case says nothing.
func TestResolvedIdentityIsNotReported(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	path := writeProjectRecord(t, cfg, "-p", "fresh",
		`{"parentUuid":null,"type":"user","uuid":"origin","sessionId":"fresh"}`)
	p := HookPayload{TranscriptPath: path, Cwd: t.TempDir()}

	id, err := stableIdentity(p)
	require.NoError(t, err)
	var out bytes.Buffer
	noteDegradedIdentity(&out, p, id)
	assert.Empty(t, out.String())
}

// TestResumeFromAnotherDirectoryResolves is the SessionStart:resume payload a
// real session resumed from a different directory got: transcript_path under
// the NEW directory's project folder, where nothing was ever written, while the
// record stayed where the session began.
func TestResumeFromAnotherDirectoryResolves(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	const sid = "06f7418e-0000-4000-8000-000000000002"
	writeProjectRecord(t, cfg, "-repo--claude-worktrees-feature", sid,
		`{"parentUuid":null,"type":"attachment","uuid":"origin","sessionId":"`+sid+`"}`)
	reported := filepath.Join(cfg, "projects", "-repo", sid+".jsonl")

	id, err := stableID(HookPayload{TranscriptPath: reported, SessionID: sid, Cwd: t.TempDir()})
	require.NoError(t, err, "a resumed session's record is where it began, whatever directory it was resumed from")
	assert.Equal(t, "origin", id)
}
