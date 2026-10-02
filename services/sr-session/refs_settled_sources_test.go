package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/repochecks"
	"github.com/sloprail/sloprail/internal/sessionpath"
)

// `refs settled` resolves the root record from the session id, so the sub-agents' old
// per-session files are read beside the repository database like everywhere else.
func TestRootRecordOf_FindsTheRootRecordAndItsSubagentsOldFiles(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ws, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	assert.Equal(t, "", rootRecordOf(ws, "root-1"), "no record yet")

	proj := sessionpath.ProjectDirOf("", ws)
	require.NoError(t, os.MkdirAll(filepath.Join(proj, "root-1", "subagents"), 0o755))
	line := func(id string) []byte {
		return []byte(`{"type":"user","uuid":"` + id + `","sessionId":"` + id + `","cwd":"` + ws + `","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"hi"}}` + "\n")
	}
	root := filepath.Join(proj, "root-1.jsonl")
	require.NoError(t, os.WriteFile(root, line("root-1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "root-1", "subagents", "agent-a1.jsonl"), line("sub-1"), 0o644))

	assert.Equal(t, root, rootRecordOf(ws, "root-1"))
	srcs := repochecks.SubagentSources(rootRecordOf(ws, "root-1"), "root-1")
	require.Len(t, srcs, 1, "the sub-agent's own old checks.db is a source of the family")
	assert.Equal(t, "a1", srcs[0].Agent)
	assert.Equal(t, "root-1", srcs[0].Family)
}
