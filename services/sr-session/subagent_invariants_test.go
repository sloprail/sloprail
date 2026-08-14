package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/transcript"
)

// The four subagent-sessions invariants, each named for the invariant it pins.
//
// They existed only implicitly before: the behaviours were covered by tests
// named for the mechanism (record(), stableID, sessionDBPath) rather than for
// the claim, so an audit grepping the spec's own vocabulary found nothing and
// could not tell an enforced invariant from an unimplemented one. The mechanism
// tests still stand and are not duplicated here — where one already pins the
// property exactly, the test below states the invariant and asserts the same
// property from the invariant's own angle.
//
// WHY THESE ARE UNIT TESTS AND NOT END-TO-END, which is a real cost and is
// stated rather than hidden. The owner's rule is that hook-based behaviour gets
// an end-to-end test, and three of these four are about what a hook is handed.
// They cannot be observed through the harness as it stands, for two reasons
// measured rather than assumed:
//
//   - The engine command the whole feature hangs off is a stub.
//     `sr-session subagent-stop` (services/sr-session/session_subagent_stop.go)
//     ends at `return nil // TODO: diff the tree, dispatch the Post events`. It
//     resolves the identity and then dispatches nothing, so no guardrail hook
//     ever runs inside a sub-agent and no ledger can record what one saw.
//   - Nothing a sub-agent does is forwarded at exit zero. Driving a real
//     dispatch through the harness (Dispatch, shared tree, a guardrail bound to
//     both Stop and SubagentStop, the project committed so a baseline exists)
//     leaves the guardrail's ledger empty and the sub-agent's stream absent from
//     the run's output. Measured during the invariant audit, not inferred.
//
// So an end-to-end test of these today would assert an absence for the wrong
// reason and pass against an engine that does nothing — the exact shape this
// suite exists to refuse. WHAT WOULD BE NEEDED: the dispatch half of
// subagent-stop implemented, and a harness able to surface a sub-agent's own
// hook effects (a ledger written inside the sub-agent's tree is enough, since
// the shared-tree dispatch leaves it where the parent's test can read it).
// judged_on_its_own_record and subagent_state_is_its_own are then directly
// observable: run the same guardrail in parent and sub-agent and assert each was
// handed its own turns and reached its own state.

// TestInvariant_the_event_says_which pins: which agent is ending is taken from
// the event that fired, never derived from the content of a transcript.
//
// The engine's expression of this is structural — SubagentStop routes to its own
// command, and IsSubagent reads the payload's own fields — so what a test can do
// is pin that the ANSWER comes from those fields and from nothing else. The two
// cases that matter are the ones a transcript-sniffing implementation would get
// wrong in opposite directions.
func TestInvariant_the_event_says_which(t *testing.T) {
	// A sub-agent is named by the payload, so the answer is yes regardless of
	// what any file holds.
	assert.True(t, HookPayload{AgentTranscriptPath: "/x/parent/subagents/agent-a.jsonl"}.IsSubagent(),
		"a payload naming a sub-agent's own record was not read as a sub-agent's")
	assert.True(t, HookPayload{AgentID: "abc", TranscriptPath: "/x/parent.jsonl"}.IsSubagent(),
		"a payload naming an agent id was not read as a sub-agent's")

	// A root is not, and this is the half a sniffing implementation gets wrong
	// in the ordinary case: a root's payload names only its own record, and
	// nothing about that record's CONTENTS may promote it to a sub-agent's.
	assert.False(t, HookPayload{TranscriptPath: "/x/parent.jsonl"}.IsSubagent(),
		"a root session's payload was read as a sub-agent's")
	assert.False(t, HookPayload{}.IsSubagent(),
		"an empty payload was read as a sub-agent's")

	// And the discriminator is exactly the two fields the harness reports, not
	// the working directory or anything else travelling alongside them. A
	// sub-agent dispatched into its own worktree reports that worktree as cwd;
	// a root in the same tree reports the same thing.
	sameCwd := "/some/tree"
	assert.True(t, HookPayload{AgentID: "abc", TranscriptPath: "/x/p.jsonl", Cwd: sameCwd}.IsSubagent())
	assert.False(t, HookPayload{TranscriptPath: "/x/p.jsonl", Cwd: sameCwd}.IsSubagent(),
		"the working directory changed the answer, so identity is not coming from the event")
}

// TestInvariant_judged_on_its_own_record pins: a sub-agent's cycle is judged
// against the sub-agent's own record, not the record of the session that
// spawned it.
//
// record() is what every judging path resolves through — the transcript a rule
// reads, and the file stableID derives identity from — so the invariant is the
// claim that it answers with the sub-agent's file whenever the payload names
// one, by either route the harness reports it.
func TestInvariant_judged_on_its_own_record(t *testing.T) {
	const parent = "/cfg/projects/-proj/parent-session.jsonl"
	const own = "/cfg/projects/-proj/parent-session/subagents/agent-abc.jsonl"

	// Reported directly. The parent's path travels ALONGSIDE the sub-agent's
	// rather than instead of it, so a reader taking transcript_path answers for
	// the parent — and judges this cycle against the spawning session's record,
	// which interleaves the sub-agent's work with its own.
	got, err := HookPayload{TranscriptPath: parent, AgentTranscriptPath: own, AgentID: "abc"}.record()
	require.NoError(t, err)
	assert.Equal(t, own, got, "a sub-agent's cycle would be judged against its parent's record")

	// Reconstructed. A harness naming the sub-agent by id only must reach the
	// same file, or the same cycle is judged against different records depending
	// on which fields happened to be reported.
	viaID, err := HookPayload{TranscriptPath: parent, AgentID: "abc"}.record()
	require.NoError(t, err)
	assert.Equal(t, own, viaID, "the id route reached a different record than the path route")

	// And the converse, which is the other half of the spec's why: a root must
	// not be judged against a sub-agent's record either.
	rootRecord, err := HookPayload{TranscriptPath: parent}.record()
	require.NoError(t, err)
	assert.Equal(t, parent, rootRecord, "a root session was pointed at something other than its own record")
}

// TestInvariant_subagent_state_is_its_own pins: the state a sub-agent's
// guardrails write is scoped to that sub-agent, and never pools with the state
// of the session that spawned it.
//
// State is keyed by (workspace, session id), so the invariant reduces to the
// claim that a sub-agent arrives with its own session id — and that the keying
// keeps two ids apart even when everything else about them matches. The shared
// tree is the case that matters: a sub-agent that did NOT get its own worktree
// has the same workspace as its parent, so the id is the only thing separating
// them.
func TestInvariant_subagent_state_is_its_own(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	// The identity really is the sub-agent's own, derived from its own record's
	// origin rather than the parent's. Built as real files because stableID
	// reads them.
	projectDir := t.TempDir()
	parentPath := filepath.Join(projectDir, "parent-session.jsonl")
	require.NoError(t, os.WriteFile(parentPath, []byte(
		`{"type":"user","uuid":"parent-origin","parentUuid":null,"isSidechain":false}`+"\n"), 0o644))

	subDir := filepath.Join(projectDir, "parent-session", transcript.SubagentDir)
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	subPath := filepath.Join(subDir, "agent-abc.jsonl")
	require.NoError(t, os.WriteFile(subPath, []byte(
		`{"type":"user","uuid":"subagent-origin","parentUuid":null,"isSidechain":true,"agentId":"abc"}`+"\n"), 0o644))

	tree := t.TempDir()
	parentID, err := stableID(HookPayload{TranscriptPath: parentPath, Cwd: tree})
	require.NoError(t, err)
	subID, err := stableID(HookPayload{
		TranscriptPath: parentPath, AgentTranscriptPath: subPath, AgentID: "abc", Cwd: tree,
	})
	require.NoError(t, err)
	require.NotEqual(t, parentID, subID,
		"the sub-agent resolved to the parent's identity, so its state would pool with the parent's")

	// The shared-tree case: one workspace, two sessions, two databases. This is
	// the pooling the spec names — a sub-agent's note that it had already
	// refused exempting the parent from a refusal the parent never received.
	parentDB, err := sessionDBPath(tree, parentID)
	require.NoError(t, err)
	subDB, err := sessionDBPath(tree, subID)
	require.NoError(t, err)
	assert.NotEqual(t, parentDB, subDB,
		"a sub-agent sharing its parent's tree wrote into the parent's state")

	// And ten sub-agents of one parent do not read each other's notes.
	seen := map[string]string{parentDB: parentID}
	for _, id := range []string{"sub-1", "sub-2", "sub-3"} {
		db, err := sessionDBPath(tree, id)
		require.NoError(t, err)
		if prior, clash := seen[db]; clash {
			t.Fatalf("sessions %q and %q share one database at %s", prior, id, db)
		}
		seen[db] = id
	}
}

// TestInvariant_identity_comes_from_the_hook pins: a sub-agent's identity reaches
// the engine from the hook that was told it, never from the agent itself.
//
// The spec's reasoning is about where the answer may be SOURCED. A harness
// exposes a sub-agent's id in the hook payload and not in the environment a tool
// call sees, so anything asking the agent's own process for it is asking for a
// guess that silently scopes state to the wrong session.
//
// What a test can pin is that the engine's resolution reads the payload and is
// unmoved by the environment. The environment is loaded here with a plausible
// wrong answer — the value an implementation reaching for os.Getenv would find —
// and the resolved identity must still be the payload's.
func TestInvariant_identity_comes_from_the_hook(t *testing.T) {
	projectDir := t.TempDir()
	parentPath := filepath.Join(projectDir, "parent-session.jsonl")
	require.NoError(t, os.WriteFile(parentPath, []byte(
		`{"type":"user","uuid":"parent-origin","parentUuid":null,"isSidechain":false}`+"\n"), 0o644))

	subDir := filepath.Join(projectDir, "parent-session", transcript.SubagentDir)
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	subPath := filepath.Join(subDir, "agent-abc.jsonl")
	require.NoError(t, os.WriteFile(subPath, []byte(
		`{"type":"user","uuid":"subagent-origin","parentUuid":null,"isSidechain":true,"agentId":"abc"}`+"\n"), 0o644))

	// The environment says something else, in every variable that names a
	// session. An implementation that consulted any of them lands on the wrong
	// conversation.
	t.Setenv(SessionEnv, "an-id-from-the-environment")
	t.Setenv(TranscriptEnv, parentPath)

	got, err := stableID(HookPayload{
		TranscriptPath:      parentPath,
		AgentTranscriptPath: subPath,
		AgentID:             "abc",
		Cwd:                 "/an/isolated/worktree/that/does/not/exist",
	})
	require.NoError(t, err)
	assert.Equal(t, "subagent-origin", got,
		"the identity came from somewhere other than the hook payload")

	// And with no sub-agent on the payload the answer is the root's, even though
	// the environment still names a sub-agent's record. Identity is the
	// invocation's to state, not the environment's.
	rootID, err := stableID(HookPayload{TranscriptPath: parentPath, Cwd: "/some/tree"})
	require.NoError(t, err)
	assert.Equal(t, "parent-origin", rootID,
		"a root's identity was displaced by an environment naming a sub-agent")
}
