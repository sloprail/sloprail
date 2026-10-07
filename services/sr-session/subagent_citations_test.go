package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// A sub-agent's pre-tool calls and its own SubagentStop must key to ONE store:
// the citations a pre-tool call records are what that sub-agent's cycle end
// attaches to its Post events. A harness may report a sub-agent's PreToolUse
// by agent_id with no transcript_path at all, and its SubagentStop by
// agent_transcript_path — both must reach the sub-agent's own record.

// citedSubagentSession writes a root record for sessionID filed under tree, and a
// sub-agent record beneath it, returning both paths.
func citedSubagentSession(t *testing.T, cfg, tree, sessionID, agentID string) (string, string) {
	t.Helper()
	dir := transcript.ProjectDir(cfg, tree)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, sessionID, transcript.SubagentDir), 0o755))
	root := filepath.Join(dir, sessionID+".jsonl")
	require.NoError(t, os.WriteFile(root, []byte(
		`{"type":"user","uuid":"root-origin","parentUuid":null,"isSidechain":false,"sessionId":"`+sessionID+`","cwd":"`+tree+`","message":{"role":"user","content":"go"}}`+"\n"), 0o644))
	sub := filepath.Join(dir, sessionID, transcript.SubagentDir, "agent-"+agentID+".jsonl")
	require.NoError(t, os.WriteFile(sub, []byte(
		`{"type":"user","uuid":"sub-origin","parentUuid":null,"isSidechain":true,"agentId":"`+agentID+`","sessionId":"`+sessionID+`","cwd":"`+tree+`","message":{"role":"user","content":"do it"}}`+"\n"), 0o644))
	return root, sub
}

func TestRecordReconstructsFromAgentIDAndSessionID(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	tree := transcript.ResolveWorkDir(t.TempDir())
	root, sub := citedSubagentSession(t, cfg, tree, "sess-1", "abc")

	// The sub-agent's PreToolUse: its agent id, the session id, no path.
	got, err := HookPayload{SessionID: "sess-1", Cwd: tree, AgentID: "abc"}.Record()
	require.NoError(t, err)
	assert.Equal(t, sub, got, "a sub-agent's call named by agent id alone resolved to the root's record")
	assert.NotEqual(t, root, got)
}

func TestSubagentPreToolAndStopShareOneStore(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tree := transcript.ResolveWorkDir(t.TempDir())
	root, sub := citedSubagentSession(t, cfg, tree, "sess-1", "abc")

	preTool := HookPayload{SessionID: "sess-1", Cwd: tree, AgentID: "abc"}
	subStop := HookPayload{SessionID: "sess-1", Cwd: tree, AgentID: "abc", TranscriptPath: sub, AgentTranscriptPath: sub}
	rootStop := HookPayload{SessionID: "sess-1", Cwd: tree, TranscriptPath: root}

	preID, err := stableID(preTool)
	require.NoError(t, err)
	stopID, err := stableID(subStop)
	require.NoError(t, err)
	rootID, err := stableID(rootStop)
	require.NoError(t, err)
	assert.Equal(t, stopID, preID, "the sub-agent's pre-tool call and its SubagentStop keyed to different sessions")
	assert.NotEqual(t, rootID, preID, "the sub-agent's pre-tool call keyed to the root session")

	// What a pre-tool call records is what the sub-agent's cycle end reads —
	// once the change it rode on has landed.
	cites := []transcript.Citation{{Quote: "q", SourceTypes: []transcript.SourceType{transcript.SourceUser}, Path: root, Line: 1}}
	abs := filepath.Join(tree, "memories", "a.md")
	store, err := openEngineState(preTool)
	require.NoError(t, err)
	point := historyPoint{Cites: cites, After: putState(store, true, "x"), Whole: true, At: 1}
	require.NoError(t, recordPending(store, []pendingChange{{Path: "memories/a.md", Abs: abs, Point: point}}))
	require.NoError(t, store.Close())

	// The root's Stop, whose difference holds the sub-agent's change in a
	// shared tree, reads it from the sub-agent's store — landed or not yet
	// settled alike, but only once the file holds what the change produces.
	got, _ := otherHistories(rootStop, root)
	assert.Empty(t, got, "a change that has not landed grounds nothing")
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte("x"), 0o644))
	want := map[string][]historyPoint{"memories/a.md": {point}}
	got, contents := otherHistories(rootStop, root)
	assert.Equal(t, want, got)
	assert.Equal(t, "x", contents[point.After.Hash], "the other store's contents come with its points")

	store, err = openEngineState(subStop)
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, settleCitedChanges(store))
	assert.Equal(t, want, historyIn(store, true))
	got, _ = otherHistories(rootStop, root)
	assert.Equal(t, want, got)
}

// A sub-agent's Stop reads, beside its own, the cited changes of the root that
// dispatched it: a file the root created with a citation and the sub-agent
// then edited with one is grounded throughout.
func TestSubagentReadsItsRootsCitedChanges(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tree := transcript.ResolveWorkDir(t.TempDir())
	root, sub := citedSubagentSession(t, cfg, tree, "sess-1", "abc")
	rootPre := HookPayload{SessionID: "sess-1", Cwd: tree, TranscriptPath: root}
	subStop := HookPayload{SessionID: "sess-1", Cwd: tree, AgentID: "abc", TranscriptPath: sub, AgentTranscriptPath: sub}

	store, err := openEngineState(rootPre)
	require.NoError(t, err)
	point := historyPoint{Cites: userCite, After: putState(store, true, "created"), Whole: true, At: 1}
	require.NoError(t, swapJSON(store, sessionstate.MetaCitations, func(all *map[string][]historyPoint) {
		*all = map[string][]historyPoint{"memories/a.md": {point}}
	}))
	require.NoError(t, store.Close())

	got, contents := otherHistories(subStop, sub)
	assert.Equal(t, map[string][]historyPoint{"memories/a.md": {point}}, got)
	assert.Equal(t, "created", contents[point.After.Hash])
}

func TestDelegatedCitationsNeverCreateAStore(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tree := transcript.ResolveWorkDir(t.TempDir())
	root, sub := citedSubagentSession(t, cfg, tree, "sess-1", "abc")

	// A sub-agent that recorded nothing under this tree (none, or isolated in
	// its own worktree): nothing is read, and no store is opened into being.
	got, _ := otherHistories(HookPayload{Cwd: tree, TranscriptPath: root}, root)
	assert.Empty(t, got)
	id, err := stableID(HookPayload{Cwd: tree, AgentTranscriptPath: sub})
	require.NoError(t, err)
	db, err := sessionDBPath(tree, id)
	require.NoError(t, err)
	_, err = os.Stat(db)
	assert.True(t, os.IsNotExist(err), "reading a sub-agent's citations created its store")
}
