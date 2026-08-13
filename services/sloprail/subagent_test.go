package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/transcript"
)

// A sub-agent is a session in its own right. These pin the two halves of that:
// which record a hook reads (so identity is the sub-agent's own), and where the
// state it keys goes (so nothing pools with the parent's).

// TestRecordPrefersTheSubagentsOwn is the routing decision. The harness reports
// the sub-agent's path ALONGSIDE the parent's, not instead of it, so a hook
// reading transcript_path alone answers for the parent — and then writes the
// sub-agent's baseline, read mark and verdicts into the parent's state.
func TestRecordPrefersTheSubagentsOwn(t *testing.T) {
	p := HookPayload{
		TranscriptPath:      "/cfg/projects/-proj/parent-session.jsonl",
		AgentTranscriptPath: "/cfg/projects/-proj/parent-session/subagents/agent-abc.jsonl",
		AgentID:             "abc",
	}
	got, err := p.record()
	require.NoError(t, err)
	assert.Equal(t, "/cfg/projects/-proj/parent-session/subagents/agent-abc.jsonl", got,
		"a sub-agent's hook read the parent's record")
}

// TestRecordReconstructsFromAgentID: when a harness names the sub-agent but not
// where it wrote, falling back to the parent's path is the confusion this file
// exists to prevent — so the path is reconstructed instead.
func TestRecordReconstructsFromAgentID(t *testing.T) {
	p := HookPayload{
		TranscriptPath: "/cfg/projects/-proj/parent-session.jsonl",
		AgentID:        "abc",
	}
	got, err := p.record()
	require.NoError(t, err)
	assert.Equal(t,
		filepath.Join("/cfg/projects/-proj/parent-session", transcript.SubagentDir, "agent-abc.jsonl"),
		got)
	assert.NotEqual(t, p.TranscriptPath, got, "the fallback silently used the parent's record")
}

// TestRecordRefusesATraversingAgentID is the comment audit on record(): its doc
// says a reconstruction cannot leave the conversation's own directory. This is
// the input that would make it, if the guard were missing or were a
// filepath.Base.
func TestRecordRefusesATraversingAgentID(t *testing.T) {
	for _, id := range []string{
		"../../../../../-another-project/victim",
		"..",
		"a/b",
		`a\b`,
	} {
		t.Run(id, func(t *testing.T) {
			p := HookPayload{
				TranscriptPath: "/cfg/projects/-a-project/session.jsonl",
				AgentID:        id,
			}
			_, err := p.record()
			require.Error(t, err, "an agent id of %q must be refused", id)
			require.ErrorIs(t, err, transcript.ErrNotAnAgentID)
		})
	}
}

// TestRecordOfARootSession: a payload naming neither sub-agent field is the
// ordinary session, which is the common case and not a fault.
func TestRecordOfARootSession(t *testing.T) {
	p := HookPayload{TranscriptPath: "/cfg/projects/-proj/session.jsonl"}
	got, err := p.record()
	require.NoError(t, err)
	assert.Equal(t, "/cfg/projects/-proj/session.jsonl", got)
	assert.False(t, p.IsSubagent())
}

// TestIsSubagentReadsWhatTheHarnessReported: either field alone is enough,
// because either is reported only to a sub-agent's hook.
func TestIsSubagentReadsWhatTheHarnessReported(t *testing.T) {
	assert.True(t, HookPayload{AgentTranscriptPath: "/x/agent-a.jsonl"}.IsSubagent())
	assert.True(t, HookPayload{AgentID: "abc"}.IsSubagent())
	assert.False(t, HookPayload{TranscriptPath: "/x/s.jsonl"}.IsSubagent())
	assert.False(t, HookPayload{}.IsSubagent())
}

// TestProjectDirOfClimbsOutOfSubagents is the property an isolated sub-agent
// depends on. Its record is nested a level deeper than a session's own, and the
// conversation's other transcripts — the ones a restart has to be crossed into —
// sit beside the session's file, two levels up.
//
// Derived from the transcript's location rather than the reported cwd precisely
// because the two disagree here: a sub-agent dispatched into its own worktree
// reports that worktree, while the harness still nests its record under the
// dispatching session's project directory. Encoding the cwd would name a
// directory the transcripts are not in.
func TestProjectDirOfClimbsOutOfSubagents(t *testing.T) {
	sub := filepath.Join("/cfg/projects/-proj/parent-session", transcript.SubagentDir, "agent-abc.jsonl")
	assert.Equal(t, "/cfg/projects/-proj", projectDirOf(sub, "/an/isolated/worktree"),
		"a sub-agent's project directory must be the dispatching session's, not its worktree's")

	own := "/cfg/projects/-proj/session.jsonl"
	assert.Equal(t, "/cfg/projects/-proj", projectDirOf(own, "/whatever"))
}

// TestProjectDirOfHandlesTheWorkflowsLayout pins the second real layout, which a
// fixed two-level climb got wrong.
//
// Claude Code writes some sub-agent records a further two levels down, under
// subagents/workflows/wf_<id>/ — 9 such files against 322 flat ones on the
// machine this was measured on. Climbing exactly two levels from one of those
// lands on <session>/subagents, a directory containing no transcripts at all, so
// every other transcript of the conversation becomes unreadable and a restart
// that should have been crossed silently is not.
//
// Latent rather than harmless: the project directory is consulted when an origin
// record carries a logicalParentUuid, and none of the sub-agents observed do.
// But TestSubagentIdentitySurvivesAFork asserts that exact case decides the
// identity, so the branch is one this codebase claims to support — and a layout
// it mishandles is not made safe by today's data happening not to reach it.
func TestProjectDirOfHandlesTheWorkflowsLayout(t *testing.T) {
	nested := filepath.Join(
		"/cfg/projects/-proj/parent-session", transcript.SubagentDir,
		"workflows", "wf_123", "agent-abc.jsonl")
	assert.Equal(t, "/cfg/projects/-proj", projectDirOf(nested, "/an/isolated/worktree"),
		"a sub-agent nested under workflows/ must resolve to the same project directory as a flat one")
}

// TestProjectDirOfIgnoresASubagentsNameAboveTheSession guards the search added
// for the layout above.
//
// Searching outwards for a directory named subagents is what makes the climb
// independent of depth, but a search can also find the WRONG one: a project
// whose own encoded path contains the component would otherwise capture it. The
// search runs deepest-first for exactly this reason, so the record's nearest
// enclosing subagents directory wins over any higher namesake.
func TestProjectDirOfIgnoresASubagentsNameAboveTheSession(t *testing.T) {
	sub := filepath.Join(
		"/cfg/projects/-proj", transcript.SubagentDir, "parent-session",
		transcript.SubagentDir, "agent-abc.jsonl")
	assert.Equal(t, filepath.Join("/cfg/projects/-proj", transcript.SubagentDir),
		projectDirOf(sub, "/whatever"),
		"the nearest enclosing subagents directory decides, not the outermost")
}

// TestProjectDirOfANonSubagentPathIsItsOwnDirectory pins the ordinary case
// against the search: a root transcript is in the project directory already, and
// nothing about it should be climbed.
func TestProjectDirOfANonSubagentPathIsItsOwnDirectory(t *testing.T) {
	assert.Equal(t, "/cfg/projects/-proj",
		projectDirOf("/cfg/projects/-proj/session.jsonl", "/whatever"))
	assert.Equal(t, "/cfg/projects/-proj",
		projectDirOf("/cfg/projects/-proj/not-subagents/../session.jsonl", "/whatever"))
}

// TestStableIDOfSubagentIsItsOwn drives the whole payload path end to end, on
// real files in the real nested layout: a parent transcript and a sub-agent's
// beside it. The two identities must differ, because everything downstream is
// keyed on them.
func TestStableIDOfSubagentIsItsOwn(t *testing.T) {
	projectDir := t.TempDir()
	parentPath := filepath.Join(projectDir, "parent-session.jsonl")
	require.NoError(t, os.WriteFile(parentPath, []byte(
		`{"type":"user","uuid":"parent-origin","parentUuid":null,"isSidechain":false}`+"\n"), 0o644))

	subDir := filepath.Join(projectDir, "parent-session", transcript.SubagentDir)
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	subPath := filepath.Join(subDir, "agent-abc.jsonl")
	require.NoError(t, os.WriteFile(subPath, []byte(
		`{"type":"user","uuid":"subagent-origin","parentUuid":null,"isSidechain":true,"agentId":"abc"}`+"\n"), 0o644))

	parentID, err := stableID(HookPayload{TranscriptPath: parentPath})
	require.NoError(t, err)
	assert.Equal(t, "parent-origin", parentID)

	// The sub-agent's hook: both paths reported, plus its own isolated cwd.
	subID, err := stableID(HookPayload{
		TranscriptPath:      parentPath,
		AgentTranscriptPath: subPath,
		AgentID:             "abc",
		Cwd:                 "/an/isolated/worktree/that/does/not/exist",
	})
	require.NoError(t, err)
	assert.Equal(t, "subagent-origin", subID)
	assert.NotEqual(t, parentID, subID,
		"the sub-agent resolved to its parent's identity — its state would land in the parent's")

	// And the reconstruction route reaches the same record.
	viaID, err := stableID(HookPayload{TranscriptPath: parentPath, AgentID: "abc"})
	require.NoError(t, err)
	assert.Equal(t, subID, viaID,
		"reconstructing from the agent id found a different session than the reported path did")
}

// TestSessionDBPathSeparatesSubagentFromParent is the state-keying decision, in
// both the cases it has to hold for.
//
// Nothing here is a sub-agent special case, which is the point: the sub-agent
// arrives with its own session id and its own cwd, and those are already the two
// coordinates the path is built from.
func TestSessionDBPathSeparatesSubagentFromParent(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tree := t.TempDir()

	parent, err := sessionDBPath(tree, "parent-origin")
	require.NoError(t, err)

	// Sharing the parent's tree: same workspace, still a different session.
	sameTree, err := sessionDBPath(tree, "subagent-origin")
	require.NoError(t, err)
	assert.NotEqual(t, parent, sameTree,
		"a sub-agent sharing the parent's tree pooled its state with the parent's")

	// Its own worktree: a different workspace as well.
	worktree := t.TempDir()
	ownTree, err := sessionDBPath(worktree, "subagent-origin")
	require.NoError(t, err)
	assert.NotEqual(t, parent, ownTree)
	assert.NotEqual(t, sameTree, ownTree,
		"the same sub-agent identity in two different trees resolved to one database")
}

// TestSessionDBPathIsStableForOneSession: the same coordinates must resolve to
// the same database on every hook, or a session loses its own state between
// cycles.
func TestSessionDBPathIsStableForOneSession(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tree := t.TempDir()
	first, err := sessionDBPath(tree, "an-origin")
	require.NoError(t, err)
	second, err := sessionDBPath(tree, "an-origin")
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

// TestSessionDBPathRefusesATraversingSessionID is the second comment audit, and
// the one guarding a bug this codebase has already shipped: an id joined onto
// the state root is cleaned AFTER concatenation, so a crafted one resolves into
// another project's state.
//
// The fixture proves the escape before asserting the refusal — a test whose
// input never reaches the path it names is the failure mode this project keeps
// hitting.
func TestSessionDBPathRefusesATraversingSessionID(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	tree := t.TempDir()

	// What the path WOULD be without the guard: out of this session's directory,
	// out of the workspace's, out of sessions/, into another project's state.
	const escape = "../../../-another-workspace/victim-session"
	unguarded := filepath.Join(data, AppName, "sessions", encodeWorkspace(tree), escape, "state.db")
	require.False(t, strings.HasPrefix(unguarded, filepath.Join(data, AppName, "sessions", encodeWorkspace(tree))+string(filepath.Separator)),
		"the fixture must actually escape this workspace's state, got %q", unguarded)

	for _, id := range []string{escape, "..", ".", "a/b", `a\b`} {
		t.Run(id, func(t *testing.T) {
			_, err := sessionDBPath(tree, id)
			require.Error(t, err, "a session id of %q must be refused, not cleaned", id)
			assert.Contains(t, err.Error(), "not a session id")
		})
	}
}

// TestSessionDBPathNeedsASessionID: an empty id must fail rather than resolve to
// the workspace directory itself, where every session would share one database.
func TestSessionDBPathNeedsASessionID(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	_, err := sessionDBPath(t.TempDir(), "")
	require.Error(t, err)
}

// TestRecordWithAgentIDButNoParentPath is the gap the two-condition fallback
// leaves, made explicit rather than left to be discovered. A payload naming an
// agent but no parent transcript cannot have a path reconstructed — there is
// nothing to nest under — so it falls through to TranscriptPath, which is empty.
//
// That empty value must not be mistaken for a resolved record. stableID refuses
// it by name, which is what this pins: the failure is loud, and specifically it
// does NOT silently become the parent's record or a path relative to nothing.
func TestRecordWithAgentIDButNoParentPath(t *testing.T) {
	p := HookPayload{AgentID: "abc"}
	got, err := p.record()
	require.NoError(t, err)
	assert.Empty(t, got, "a reconstruction with nothing to nest under must not invent a path")

	_, err = stableID(p)
	require.Error(t, err, "an unresolvable record must fail loudly, never resolve to something else")
	assert.Contains(t, err.Error(), "no transcript path")
}

// TestStableIDRefusesATraversingAgentIDEndToEnd: the guard has to hold on the
// path the engine actually takes, not only where it is implemented. A hook
// payload is the only input an attacker-shaped value arrives on.
func TestStableIDRefusesATraversingAgentIDEndToEnd(t *testing.T) {
	_, err := stableID(HookPayload{
		TranscriptPath: "/cfg/projects/-a-project/session.jsonl",
		AgentID:        "../../../../../-another-project/victim",
	})
	require.Error(t, err)
	require.ErrorIs(t, err, transcript.ErrNotAnAgentID)
}
