package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// A Stop whose ranges raise no citation refusal must not touch the sub-agents' stores: the
// recorded-quotes hint is built only when a refusal needs it. Once it is built it is built
// once, however many ranges ask.
func TestStopWithManySubagentsDoesNoPerSubagentWork(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tree := transcript.ResolveWorkDir(t.TempDir())
	root, _ := citedSubagentSession(t, cfg, tree, "sess-1", "a0")
	dir := filepath.Join(filepath.Dir(root), "sess-1", transcript.SubagentDir)
	for i := 1; i < 200; i++ {
		src, err := os.ReadFile(filepath.Join(dir, "agent-a0.jsonl"))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-a"+string(rune('a'+i%26))+string(rune('a'+i/26))+".jsonl"), src, 0o644))
	}
	p := HookPayload{SessionID: "sess-1", Cwd: tree, TranscriptPath: root}

	before := otherStoreVisits.Load()
	recorded := lazyRecordedCitations(p, nil)
	assert.Equal(t, before, otherStoreVisits.Load(), "building the lazy hint did work before anyone asked")

	start := time.Now()
	recorded()
	recorded()
	recorded()
	assert.Less(t, time.Since(start), 10*time.Second)
	assert.LessOrEqual(t, otherStoreVisits.Load()-before, int64(200), "asked three times, the hint was built more than once")
	once := otherStoreVisits.Load() - before
	recorded()
	assert.Equal(t, once, otherStoreVisits.Load()-before, "the hint was rebuilt on a later ask")
}

func TestVerifyRangeWithoutRefusalNeverBuildsRecordedHint(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "a.md"), []byte("a"), 0o644))
	runGit(t, proj, "add", "a.md")
	runGit(t, proj, "commit", "-m", "init")
	base := runGit(t, proj, "rev-parse", "HEAD")
	head := runGit(t, proj, "rev-parse", "--abbrev-ref", "HEAD")
	cmd := &cobra.Command{}
	built := 0
	got := verifyRangeWith(cmd, HookPayload{Cwd: proj}, nil, cmd, sessionstate.TrackedRange{Folder: proj, Head: head, Base: base},
		func() map[string][]transcript.Citation { built++; return nil }, nil, nil)
	_ = got
	assert.Zero(t, built)
}

// The git root of a directory is asked of git once per process.
func TestWorkspaceAnchorIsMemoized(t *testing.T) {
	proj := initRepo(t)
	first := sessionpath.WorkspaceAnchor(proj)
	start := time.Now()
	for i := 0; i < 5000; i++ {
		require.Equal(t, first, sessionpath.WorkspaceAnchor(proj))
	}
	assert.Less(t, time.Since(start), 2*time.Second, "5000 anchors cost a git process each")
}
