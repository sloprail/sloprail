package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These fixtures write the real layout Claude Code produces — a root transcript
// whose assistant turn dispatches a sub-agent by a tool_use id, and beside it a
// <session>/subagents/agent-<id>.jsonl record with an agent-<id>.meta.json
// companion carrying that same toolUseId. That companion, and the id in it, are
// what the parent correlation is derived from; a fake would define away the two
// things that actually go wrong — whether the meta file is found, and whether its
// id matches a tool_use in the parent.

// dispatchRecord is an assistant turn dispatching a sub-agent: a tool_use block
// with the Agent tool and the given id, which is the id the sub-agent's meta file
// points back at.
func dispatchRecord(uuid, parent, toolUseID string) string {
	return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":"` + parent + `","isSidechain":false,` +
		`"timestamp":"2026-08-13T09:02:00.000Z","message":{"role":"assistant","content":[` +
		`{"type":"text","text":"delegating now"},` +
		`{"type":"tool_use","id":"` + toolUseID + `","name":"Agent","input":{"description":"go"}}` +
		`]}}`
}

// writeSubagentMeta writes the companion meta file beside a sub-agent's record,
// in the shape Claude Code writes it.
func (p *project) writeSubagentMeta(parentName, agentID string, m SubagentMeta) {
	p.t.Helper()
	dir := filepath.Join(p.dir, parentName, SubagentDir)
	require.NoError(p.t, os.MkdirAll(dir, 0o755))
	b, err := json.Marshal(m)
	require.NoError(p.t, err)
	path := filepath.Join(dir, "agent-"+agentID+metaSuffix)
	require.NoError(p.t, os.WriteFile(path, b, 0o644), "fixture: write %s", path)
}

// TestParentPathCorrelatesThroughTheMetaToolUseID is the heart of describe: a
// sub-agent's immediate parent is derived, not stored — its meta file names the
// toolUseId that dispatched it, and the parent is whichever trajectory holds a
// tool_use with that id.
func TestParentPathCorrelatesThroughTheMetaToolUseID(t *testing.T) {
	p := newProject(t)
	const dispatchID = "toolu_01parentdispatch"
	parent := p.write("the-parent",
		preamble(),
		root("parent-origin"),
		dispatchRecord("parent-dispatch", "parent-origin", dispatchID),
	)
	sub := p.writeSubagent("the-parent", "agent1",
		sidechainRoot("subagent-origin", "agent1", "the-parent"),
		sidechainRecord("subagent-work", "subagent-origin", "agent1"),
	)
	p.writeSubagentMeta("the-parent", "agent1", SubagentMeta{
		AgentType: "Explore", Description: "look into X", ToolUseID: dispatchID, SpawnDepth: 1,
	})

	got, err := ParentPath(sub, p.dir)
	require.NoError(t, err, "ParentPath")
	assert.Equal(t, parent, got,
		"the parent is the trajectory holding the tool_use the meta's toolUseId names")
}

// TestParentPathNestedSubagentIsImmediateNotRoot: the parent is the IMMEDIATE
// dispatcher, which may itself be a sub-agent at depth > 1 — not the root. A
// sub-agent dispatched by another sub-agent must resolve to that middle
// sub-agent's record, since that is where its dispatching tool_use lives.
func TestParentPathNestedSubagentIsImmediateNotRoot(t *testing.T) {
	p := newProject(t)

	// Root dispatches the middle sub-agent.
	const midDispatchID = "toolu_mid_dispatch"
	p.write("the-parent",
		root("root-origin"),
		dispatchRecord("root-dispatch", "root-origin", midDispatchID),
	)
	middle := p.writeSubagent("the-parent", "middleagent",
		sidechainRoot("mid-origin", "middleagent", "the-parent"),
		// The middle sub-agent, in turn, dispatches a leaf — the tool_use for the
		// leaf lives HERE, in the middle sub-agent's own record.
		dispatchRecord("mid-dispatch", "mid-origin", "toolu_leaf_dispatch"),
	)
	p.writeSubagentMeta("the-parent", "middleagent", SubagentMeta{
		ToolUseID: midDispatchID, SpawnDepth: 1,
	})

	// The leaf sub-agent is nested under the MIDDLE sub-agent's own subagents
	// directory, and its meta names the tool_use in the middle sub-agent.
	leafDir := filepath.Join(p.dir, "the-parent", SubagentDir, "middleagent")
	require.NoError(t, os.MkdirAll(filepath.Join(leafDir, SubagentDir), 0o755))
	leaf := filepath.Join(leafDir, SubagentDir, "agent-leafagent.jsonl")
	require.NoError(t, os.WriteFile(leaf, []byte(
		sidechainRoot("leaf-origin", "leafagent", "the-parent")+"\n"), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(leafDir, SubagentDir, "agent-leafagent"+metaSuffix),
		[]byte(`{"toolUseId":"toolu_leaf_dispatch","spawnDepth":2}`), 0o644))

	got, err := ParentPath(leaf, p.dir)
	require.NoError(t, err, "ParentPath(leaf)")
	assert.Equal(t, middle, got,
		"a nested sub-agent's parent is the sub-agent that dispatched it, not the root")
}

// TestParentPathIsEmptyForARoot: a root trajectory has no meta file, so there is
// no dispatching id to correlate and no parent to name — "" with no error.
func TestParentPathIsEmptyForARoot(t *testing.T) {
	p := newProject(t)
	root := p.write("the-parent", root("parent-origin"), record("r2", "parent-origin"))

	got, err := ParentPath(root, p.dir)
	require.NoError(t, err)
	assert.Empty(t, got, "a root has no meta file and so no parent to derive")
}

// TestParentPathIsEmptyWhenTheMetaHasNoToolUseID: a meta file that records no
// toolUseId — the shape the mock and some harnesses write — cannot be
// correlated, and the honest answer is no parent rather than a guess.
func TestParentPathIsEmptyWhenTheMetaHasNoToolUseID(t *testing.T) {
	p := newProject(t)
	sub := p.writeSubagent("the-parent", "agent1", sidechainRoot("subagent-origin", "agent1", "the-parent"))
	p.writeSubagentMeta("the-parent", "agent1", SubagentMeta{AgentType: "general-purpose", ToolUseID: ""})

	got, err := ParentPath(sub, p.dir)
	require.NoError(t, err)
	assert.Empty(t, got, "a meta without a toolUseId cannot name a parent")
}

// TestParentPathIsEmptyWhenNoTrajectoryHoldsTheID: a meta naming a toolUseId that
// no trajectory in the session holds — the dispatching call is somewhere the
// search cannot see — returns "" rather than inventing a link.
func TestParentPathIsEmptyWhenNoTrajectoryHoldsTheID(t *testing.T) {
	p := newProject(t)
	p.write("the-parent", root("parent-origin"), record("r2", "parent-origin"))
	sub := p.writeSubagent("the-parent", "agent1", sidechainRoot("subagent-origin", "agent1", "the-parent"))
	p.writeSubagentMeta("the-parent", "agent1", SubagentMeta{ToolUseID: "toolu_nowhere"})

	got, err := ParentPath(sub, p.dir)
	require.NoError(t, err)
	assert.Empty(t, got, "a toolUseId no trajectory holds names no parent")
}

// TestParentPathDoesNotReturnItself: the sub-agent's own record is skipped in the
// search, so a sub-agent whose meta id somehow also appears in its own file is
// never reported as its own parent.
func TestParentPathDoesNotReturnItself(t *testing.T) {
	p := newProject(t)
	const dispatchID = "toolu_self_ref"
	parent := p.write("the-parent",
		root("parent-origin"),
		dispatchRecord("parent-dispatch", "parent-origin", dispatchID),
	)
	// The sub-agent's own record ALSO contains a tool_use with the same id (a
	// contrived but defensible collision). The parent must still be the parent.
	sub := p.writeSubagent("the-parent", "agent1",
		sidechainRoot("subagent-origin", "agent1", "the-parent"),
		dispatchRecord("sub-has-it-too", "subagent-origin", dispatchID),
	)
	p.writeSubagentMeta("the-parent", "agent1", SubagentMeta{ToolUseID: dispatchID})

	got, err := ParentPath(sub, p.dir)
	require.NoError(t, err)
	assert.Equal(t, parent, got, "the search skips the sub-agent's own file, so it never cites itself")
}

// TestSubagentPathsEnumeratesTheSubagentDirectory: the inverse correlation — a
// root names the sub-agents it spawned by enumerating <session>/subagents, and
// returns only the agent-<id>.jsonl records, not their meta companions.
func TestSubagentPathsEnumeratesTheSubagentDirectory(t *testing.T) {
	p := newProject(t)
	root := p.write("the-parent", root("parent-origin"))
	one := p.writeSubagent("the-parent", "agentone", sidechainRoot("o1", "agentone", "the-parent"))
	two := p.writeSubagent("the-parent", "agenttwo", sidechainRoot("o2", "agenttwo", "the-parent"))
	// Meta companions sit beside them and must NOT be returned as transcripts.
	p.writeSubagentMeta("the-parent", "agentone", SubagentMeta{ToolUseID: "x"})
	p.writeSubagentMeta("the-parent", "agenttwo", SubagentMeta{ToolUseID: "y"})

	got, err := SubagentPaths(root)
	require.NoError(t, err, "SubagentPaths")
	assert.ElementsMatch(t, []string{one, two}, got,
		"only the agent-<id>.jsonl records, not the meta companions")
}

// TestSubagentPathsIsEmptyWithNoSubagentDir: a trajectory that spawned nothing
// has no subagents directory, which is a plain empty result rather than a fault.
func TestSubagentPathsIsEmptyWithNoSubagentDir(t *testing.T) {
	p := newProject(t)
	root := p.write("lonely", root("origin"))

	got, err := SubagentPaths(root)
	require.NoError(t, err)
	assert.Empty(t, got, "no subagents directory means no sub-agents, not an error")
}

// TestIsSubagentTranscriptByMetaCompanion: a record with a meta file beside it is
// a sub-agent's, settled by the companion's presence without reading the record.
func TestIsSubagentTranscriptByMetaCompanion(t *testing.T) {
	p := newProject(t)
	sub := p.writeSubagent("the-parent", "agent1",
		// Deliberately WITHOUT isSidechain on the record, to prove the meta file
		// alone is decisive.
		`{"type":"user","uuid":"u1","parentUuid":null,"isSidechain":false,"message":{"role":"user","content":"go"}}`,
	)
	p.writeSubagentMeta("the-parent", "agent1", SubagentMeta{ToolUseID: "x"})

	assert.True(t, IsSubagentTranscript(sub), "a meta companion marks a sub-agent transcript")
}

// TestIsSubagentTranscriptByIsSidechain: with no meta file, a record whose own
// entries carry isSidechain is still a sub-agent's — the ground truth the spec
// names, and what a harness that inlines has instead of a separate file.
func TestIsSubagentTranscriptByIsSidechain(t *testing.T) {
	p := newProject(t)
	// A plain top-level file (no subagents nesting, no meta) whose root record is
	// a sidechain.
	sub := p.write("looks-like-a-sidechain",
		sidechainRoot("origin", "agentx", "the-parent"),
	)
	assert.True(t, IsSubagentTranscript(sub), "isSidechain on the record marks a sub-agent transcript")
}

// TestIsSubagentTranscriptFalseForARoot: an ordinary root transcript — no meta,
// no sidechain — is not a sub-agent's.
func TestIsSubagentTranscriptFalseForARoot(t *testing.T) {
	p := newProject(t)
	root := p.write("a-root", preamble(), root("origin"), record("r2", "origin"))
	assert.False(t, IsSubagentTranscript(root), "a root transcript is not a sub-agent's")
}

// TestReadSubagentMetaAbsentIsNotAnError: a root has no meta file, and that is a
// plain "no" (ok=false, err=nil), not an error to handle.
func TestReadSubagentMetaAbsentIsNotAnError(t *testing.T) {
	p := newProject(t)
	root := p.write("a-root", root("origin"))
	_, ok, err := ReadSubagentMeta(root)
	require.NoError(t, err)
	assert.False(t, ok, "a missing meta file is 'no meta', not an error")
}

// TestReadSubagentMetaMalformedIsAnError: a meta file that exists but will not
// parse is the harness having written something unexpected — surfaced, not
// swallowed into "no meta".
func TestReadSubagentMetaMalformedIsAnError(t *testing.T) {
	p := newProject(t)
	sub := p.writeSubagent("the-parent", "agent1", sidechainRoot("o", "agent1", "the-parent"))
	// Corrupt the meta companion.
	metaPath := filepath.Join(p.dir, "the-parent", SubagentDir, "agent-agent1"+metaSuffix)
	require.NoError(t, os.WriteFile(metaPath, []byte("{ not json"), 0o644))

	_, ok, err := ReadSubagentMeta(sub)
	require.Error(t, err, "a malformed meta file is an error")
	assert.False(t, ok)
}

// TestReadSubagentMetaParsesTheRealShape pins the field mapping against the exact
// bytes Claude Code writes, so a rename of a json tag is caught here.
func TestReadSubagentMetaParsesTheRealShape(t *testing.T) {
	p := newProject(t)
	sub := p.writeSubagent("the-parent", "agent1", sidechainRoot("o", "agent1", "the-parent"))
	metaPath := filepath.Join(p.dir, "the-parent", SubagentDir, "agent-agent1"+metaSuffix)
	// Verbatim from a real agent-<id>.meta.json.
	require.NoError(t, os.WriteFile(metaPath, []byte(
		`{"agentType":"Explore","description":"Find X","toolUseId":"toolu_01SXSiDCznzQRW5TuHWxiPv5","spawnDepth":1}`), 0o644))

	m, ok, err := ReadSubagentMeta(sub)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "Explore", m.AgentType)
	assert.Equal(t, "Find X", m.Description)
	assert.Equal(t, "toolu_01SXSiDCznzQRW5TuHWxiPv5", m.ToolUseID)
	assert.Equal(t, 1, m.SpawnDepth)
}
