package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sidechainRoot is the shape a sub-agent's transcript actually opens with: a
// parentless record of its own, carrying isSidechain and the agent's id. Taken
// from a real one rather than invented — every one of the 331 on the machine
// this was measured on looks like this, in both of the layouts they are written
// in (see SessionDirOfSubagent).
func sidechainRoot(uuid, agentID, parentSessionID string) string {
	return `{"type":"user","uuid":"` + uuid + `","parentUuid":null,"isSidechain":true,` +
		`"agentId":"` + agentID + `","sessionId":"` + parentSessionID + `",` +
		`"timestamp":"2026-08-13T09:00:00.000Z","message":{"role":"user","content":"go and do the thing"}}`
}

// sidechainRecord is an ordinary record inside a sub-agent's transcript.
func sidechainRecord(uuid, parent, agentID string) string {
	return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":"` + parent + `","isSidechain":true,` +
		`"agentId":"` + agentID + `","timestamp":"2026-08-13T09:01:00.000Z",` +
		`"message":{"role":"assistant","content":"doing it"}}`
}

// TestSubagentIdentityIsItsOwnOrigin is the decision this file rests on: a
// sub-agent's identity comes from its OWN origin record, and StableSessionID
// needs no extension to produce it.
//
// The two must differ. If a sub-agent resolved to its parent's identity, its
// baseline, its read mark and its verdicts would all be written into the
// parent's state — and a sub-agent working in its own worktree would be judging
// different content at the same paths, so those verdicts would be wrong for the
// parent rather than merely shared with it.
func TestSubagentIdentityIsItsOwnOrigin(t *testing.T) {
	p := newProject(t)
	parent := p.write("the-parent",
		preamble(),
		root("parent-origin"),
		record("dispatched-a-subagent", "parent-origin"),
	)
	// The sub-agent's own file, in its own directory beside the parent's.
	sub := p.writeSubagent("the-parent", "a6e302233716d506d",
		sidechainRoot("subagent-origin", "a6e302233716d506d", "the-parent"),
		sidechainRecord("subagent-work", "subagent-origin", "a6e302233716d506d"),
	)

	parentID, err := StableSessionID(p.dir, parent)
	require.NoError(t, err, "StableSessionID(parent)")
	subID, err := StableSessionID(p.dir, sub)
	require.NoError(t, err, "StableSessionID(sub-agent)")

	assert.Equal(t, "parent-origin", parentID)
	assert.Equal(t, "subagent-origin", subID,
		"a sub-agent's identity must come from its own origin record")
	assert.NotEqual(t, parentID, subID,
		"a sub-agent resolved to its parent's identity — its state would be written into the parent's")
}

// TestSubagentIdentitySurvivesAFork is the property the whole session-identity
// machinery exists for, asked of a sub-agent. A sub-agent's transcript re-forked
// mid-run must still resolve to the same identity, or the sub-agent loses its
// baseline and every verdict partway through exactly as a root session would.
//
// Not observed in the wild — a key census over all 331 real sub-agent
// transcripts (35,825 records) found no logicalParentUuid on any of them — but
// it is the same file format written by the same
// harness, and a sub-agent long enough to compact is the case where losing state
// costs the most. Pinned so that whatever makes a root session survive a fork
// keeps working here.
func TestSubagentIdentitySurvivesAFork(t *testing.T) {
	p := newProject(t)
	shared := sidechainRoot("subagent-origin", "agent1", "the-parent")

	before := p.writeSubagent("the-parent", "agent1",
		shared,
		sidechainRecord("work-before", "subagent-origin", "agent1"),
	)
	forked := p.writeSubagent("the-parent", "agent1-forked",
		shared,
		sidechainRecord("work-before", "subagent-origin", "agent1"),
		sidechainRecord("work-after", "work-before", "agent1"),
	)

	first, err := StableSessionID(p.dir, before)
	require.NoError(t, err)
	second, err := StableSessionID(p.dir, forked)
	require.NoError(t, err)
	assert.Equal(t, first, second,
		"a re-forked sub-agent transcript changed identity — its state is now unreachable")
}

// TestTwoSubagentsOfOneParentAreDistinct: a session dispatching two sub-agents
// has three sessions in play, not two. Were the two to collide, one sub-agent's
// verdicts would exempt the other's files — and if they hold separate worktrees
// those are different bytes at the same path.
func TestTwoSubagentsOfOneParentAreDistinct(t *testing.T) {
	p := newProject(t)
	one := p.writeSubagent("the-parent", "agentone", sidechainRoot("origin-one", "agentone", "the-parent"))
	two := p.writeSubagent("the-parent", "agenttwo", sidechainRoot("origin-two", "agenttwo", "the-parent"))

	first, err := StableSessionID(p.dir, one)
	require.NoError(t, err)
	second, err := StableSessionID(p.dir, two)
	require.NoError(t, err)
	assert.NotEqual(t, first, second, "two sub-agents of one parent share an identity")
}

// TestSubagentTranscriptPathLayout pins the reconstruction against the layout
// measured on real transcripts.
func TestSubagentTranscriptPathLayout(t *testing.T) {
	got, err := SubagentTranscriptPath("/cfg/projects/-a-project/7173666c.jsonl", "a6e302233716d506d")
	require.NoError(t, err)
	assert.Equal(t,
		filepath.Join("/cfg/projects/-a-project/7173666c", "subagents", "agent-a6e302233716d506d.jsonl"),
		got)
}

// TestSubagentTranscriptPathRefusesTraversal is the comment audit. The doc on
// SubagentTranscriptPath says an agent id carrying a separator cannot reach
// another conversation's directory; these are the inputs that would make it
// happen if the guard were absent or were a filepath.Base.
//
// The first case is the real one: filepath.Join CLEANS after concatenating, so
// without the guard "../../.." walks straight up out of the subagents directory,
// out of the session directory, out of the project directory, and lands the read
// — and then the identity, and then everything keyed on it — in another
// project's state.
func TestSubagentTranscriptPathRefusesTraversal(t *testing.T) {
	parent := "/cfg/projects/-a-project/7173666c.jsonl"

	// Proof the escape is real rather than theoretical: this is what the path
	// would be if the id were simply joined.
	//
	// Five hops, not three, and the reason is worth writing down — the "agent-"
	// prefix makes "agent-.." one ordinary component, so the first ".." is spent
	// undoing the prefix rather than climbing. The remaining four leave the
	// subagents directory, the session directory, the project directory, and
	// finally projects/ itself, landing in a SIBLING project. That is another
	// conversation's state, read and then keyed against, with no error anywhere.
	const escape = "../../../../../-another-project/victim"
	escaped := filepath.Join(strings.TrimSuffix(parent, ".jsonl"), SubagentDir,
		"agent-"+escape+".jsonl")
	require.Equal(t, "/cfg/projects/-another-project/victim.jsonl", escaped,
		"the fixture must land in another project's directory to be worth guarding against")
	require.False(t, strings.HasPrefix(escaped, "/cfg/projects/-a-project/"),
		"the fixture must actually escape this project, got %q", escaped)

	for _, id := range []string{
		escape,
		"../../../-another-project/victim",
		"..",
		".",
		"a/b",
		`a\b`,
		"../sibling",
		`..\sibling`,
		"",
	} {
		t.Run("id="+id, func(t *testing.T) {
			_, err := SubagentTranscriptPath(parent, id)
			require.Error(t, err, "an agent id of %q must be refused, not repaired", id)
			require.ErrorIs(t, err, ErrNotAnAgentID)
		})
	}
}

// TestSubagentTranscriptPathNeedsAParent: an assembled path missing a piece is a
// guess, and a guess about where state lives puts one session's work under
// another's name.
func TestSubagentTranscriptPathNeedsAParent(t *testing.T) {
	_, err := SubagentTranscriptPath("", "agent1")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNoTranscriptPath)
}

// writeSubagent puts a sub-agent's transcript where a harness nests it —
// <parent>/subagents/agent-<id>.jsonl — and returns its path. The real layout,
// so the identity walk is exercised against the directory structure it will
// actually meet rather than a flattened stand-in.
func (p *project) writeSubagent(parentName, agentID string, lines ...string) string {
	p.t.Helper()
	dir := filepath.Join(p.dir, parentName, SubagentDir)
	require.NoError(p.t, os.MkdirAll(dir, 0o755), "fixture: mkdir %s", dir)
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	path := filepath.Join(dir, "agent-"+agentID+".jsonl")
	require.NoError(p.t, os.WriteFile(path, []byte(body), 0o644), "fixture: write %s", path)
	return path
}
