package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// runSubagentStop drives the command with a payload on stdin, as a harness does.
func runSubagentStop(t *testing.T, payload string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newSessionSubagentStopCmd()
	var out, errOut bytes.Buffer
	cmd.SetIn(strings.NewReader(payload))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// TestSubagentStopNeverBlocksOnAnUnplaceableCycle is the property that keeps a
// stub from bricking delegation.
//
// A non-zero exit from SubagentStop is a BLOCK: the harness re-runs the
// sub-agent's turn. So a failure that will not change on a retry — a payload
// naming no sub-agent, a record that cannot be read — must not be returned as an
// error, or every delegated task is trapped in a loop it cannot leave.
//
// Declining to judge is the safe failure here. Refusing to let work finish is
// not: a guardrail engine that bricks delegation is worse than one that says so
// and stands down.
func TestSubagentStopNeverBlocksOnAnUnplaceableCycle(t *testing.T) {
	for _, tc := range []struct{ name, payload, wants string }{
		{
			name:    "no sub-agent on the payload",
			payload: `{"transcript_path":"/nowhere/session.jsonl"}`,
			wants:   "not judging this cycle",
		},
		{
			name:    "a sub-agent whose record cannot be read",
			payload: `{"transcript_path":"/nowhere/session.jsonl","agent_id":"abc","agent_transcript_path":"/nowhere/subagents/agent-abc.jsonl"}`,
			wants:   "went unjudged",
		},
		{
			name:    "an agent id that is not a name",
			payload: `{"transcript_path":"/nowhere/session.jsonl","agent_id":"../../elsewhere"}`,
			wants:   "went unjudged",
		},
		{
			name:    "nothing at all",
			payload: `{}`,
			wants:   "not judging this cycle",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, err := runSubagentStop(t, tc.payload)
			require.NoError(t, err,
				"a non-zero exit here blocks the sub-agent and the harness re-runs its turn — an unplaceable cycle would loop forever")
			assert.Contains(t, stderr, tc.wants,
				"standing down must be reported, or the gap is invisible")
		})
	}
}

// TestSubagentStopStandsDownRatherThanActingAsTheParent: the one thing that must
// never happen is a sub-agent's cycle being judged under the identity of the
// session that dispatched it. A payload naming no sub-agent gets no judgement at
// all, rather than the parent's.
func TestSubagentStopStandsDownRatherThanActingAsTheParent(t *testing.T) {
	_, stderr, err := runSubagentStop(t, `{"transcript_path":"/nowhere/session.jsonl"}`)
	require.NoError(t, err)
	assert.Contains(t, stderr, "rather than judging it as the session that dispatched it")
}

// TestSubagentStopHonoursStopHookActive: already refused once this cycle, so
// refusing again would be a loop the sub-agent cannot leave.
//
// The command's own doc cites this test as the contract for doing NOTHING here
// — not even discarding the read position the way stop's interrupted path does
// — so it is worth being precise about what the test actually pins, and what it
// leaves open.
//
// Pinned: no second refusal, and nothing on the block channel. Those are the
// deadlock conditions, and they are what the assertions below check.
//
// NOT pinned, and deliberately no longer implied: silence. An earlier version
// asserted an empty stderr, which quietly asserted that this path does no
// bookkeeping at all — a much stronger claim than the deadlock one, and the
// reason a later attempt to add the discard here read as a regression rather
// than as a decision to weigh.
//
// The open consequence, named rather than left implicit: a sub-agent whose
// cycle was refused leaves MetaTranscriptOffered standing. On the root that
// stale position is cleared precisely because a later cycle which queries
// nothing would otherwise take the mark from it and declare turns judged that
// nothing read. The same hazard exists for a sub-agent that goes round again
// after a refusal. It is not closed here because closing it means opening the
// store on a path whose whole purpose is to do nothing — a real trade rather
// than an oversight, and one worth revisiting if a sub-agent is ever observed
// advancing its mark over turns it never saw.
func TestSubagentStopHonoursStopHookActive(t *testing.T) {
	stdout, _, err := runSubagentStop(t,
		`{"transcript_path":"/nowhere/s.jsonl","agent_id":"abc","stop_hook_active":true}`)
	require.NoError(t, err, "a second refusal would be a loop the sub-agent cannot leave")
	assert.Empty(t, stdout,
		"nothing may be written to the block channel on a cycle that was already refused once")
}

// subagentSession builds a real parent transcript with a sub-agent's record
// nested beneath it, and returns a payload naming the sub-agent's cycle in tree.
//
// Real files because stableID reads them: a fabricated payload would exercise
// the routing against a shape no harness writes.
func subagentSession(t *testing.T, tree string) HookPayload {
	t.Helper()
	projectDir := t.TempDir()
	parentPath := filepath.Join(projectDir, "parent-session.jsonl")
	require.NoError(t, os.WriteFile(parentPath, []byte(
		`{"type":"user","uuid":"parent-origin","parentUuid":null,"isSidechain":false}`+"\n"), 0o644))

	subDir := filepath.Join(projectDir, "parent-session", transcript.SubagentDir)
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	subPath := filepath.Join(subDir, "agent-abc.jsonl")
	require.NoError(t, os.WriteFile(subPath, []byte(
		`{"type":"user","uuid":"subagent-origin","parentUuid":null,"isSidechain":true,"agentId":"abc"}`+"\n"), 0o644))

	return HookPayload{
		TranscriptPath:      parentPath,
		AgentTranscriptPath: subPath,
		AgentID:             "abc",
		Cwd:                 tree,
	}
}

// runSubagentStopWith drives the command with a payload value rather than raw
// JSON, so a test can point it at real files on disk.
func runSubagentStopWith(t *testing.T, p HookPayload) (stdout, stderr string, err error) {
	t.Helper()
	body, mErr := json.Marshal(p)
	require.NoError(t, mErr)
	return runSubagentStop(t, string(body))
}

// TestSubagentStopTakesTheBaselineInItsOwnStore is the hole this command had:
// it resolved the sub-agent's identity and then did nothing with it, so a
// sub-agent's cycle ended with no point to measure the next one's difference
// from.
//
// Asserted by reading the sub-agent's OWN store afterwards. That is the whole
// claim in one: a baseline exists, and it exists where the sub-agent's state
// lives rather than where the parent's does.
func TestSubagentStopTakesTheBaselineInItsOwnStore(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tree := initRepo(t)
	commitFile(t, tree, "seed.txt", "seed")

	p := subagentSession(t, tree)
	_, _, err := runSubagentStopWith(t, p)
	require.NoError(t, err, "the cycle must not block")

	subID, err := stableID(p)
	require.NoError(t, err)
	dbPath, err := sessionDBPath(tree, subID)
	require.NoError(t, err)

	store, err := sessionstate.Open(dbPath)
	require.NoError(t, err, "the sub-agent's own store was never created — no baseline was taken")
	defer store.Close()

	commit, ok, err := store.Meta(sessionstate.MetaBaselineCommit)
	require.NoError(t, err)
	require.True(t, ok, "no baseline recorded: the sub-agent's cycle measured from nothing")
	assert.NotEmpty(t, commit)

	// And it is the sub-agent's store, not the parent's. Were the point written
	// under the parent's identity, the parent's next cycle would measure from a
	// point the sub-agent took.
	parentID, err := stableID(HookPayload{TranscriptPath: p.TranscriptPath, Cwd: tree})
	require.NoError(t, err)
	require.NotEqual(t, parentID, subID)
	parentDB, err := sessionDBPath(tree, parentID)
	require.NoError(t, err)
	_, statErr := os.Stat(parentDB)
	assert.True(t, os.IsNotExist(statErr),
		"subagent-stop wrote into the dispatching session's store at %s", parentDB)
}

// TestSubagentStopDispatchesThePostEvents pins the other half. A baseline with
// no dispatch is a cycle that measured and judged nothing.
//
// Driven by standing in for the dispatch step, the same seam the root's own
// tests use — so this asserts that subagent-stop REACHES dispatch, which is
// what was missing, rather than re-testing what dispatch does.
func TestSubagentStopDispatchesThePostEvents(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tree := initRepo(t)
	commitFile(t, tree, "seed.txt", "seed")

	original := natureStopDispatch
	t.Cleanup(func() { natureStopDispatch = original })

	var dispatched bool
	var gotSession string
	natureStopDispatch = func(cmd *cobra.Command, p HookPayload) string {
		dispatched = true
		if id, err := stableID(p); err == nil {
			gotSession = id
		}
		return ""
	}

	p := subagentSession(t, tree)
	_, _, err := runSubagentStopWith(t, p)
	require.NoError(t, err)

	require.True(t, dispatched,
		"a sub-agent's cycle ended without dispatching anything — delegated work goes unguarded")
	assert.Equal(t, "subagent-origin", gotSession,
		"the cycle was dispatched under the wrong session's identity")
}

// TestSubagentStopBlocksWhenAGuardrailRefuses: a refusal at a sub-agent's own
// cycle must reach the block channel, which is what makes the harness re-run
// the sub-agent's turn with the feedback.
//
// Measured rather than assumed — see this package's note on the mock's
// SubagentStop loop. Both refusal channels are observable through the harness,
// contrary to what an earlier measurement recorded.
func TestSubagentStopBlocksWhenAGuardrailRefuses(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tree := initRepo(t)
	commitFile(t, tree, "seed.txt", "seed")

	original := natureStopDispatch
	t.Cleanup(func() { natureStopDispatch = original })
	natureStopDispatch = func(cmd *cobra.Command, p HookPayload) string {
		// What the Stop dispatch returns when a rule refuses: the block text.
		// completeCycle writes it to the block channel and holds the mark.
		return "a rule refused"
	}

	p := subagentSession(t, tree)
	stdout, _, err := runSubagentStopWith(t, p)
	require.NoError(t, err,
		"a refusal travels on the block channel, never as a non-zero exit")
	assert.Contains(t, stdout, `"decision":"block"`,
		"a guardrail refused a sub-agent's cycle and nothing stopped the turn")
	assert.Contains(t, stdout, "a rule refused")
}

// TestSubagentStopAdvancesTheMarkOnACompletedCycle is the positive half, and it
// is what makes the negative half below mean anything.
//
// Asserting only that the mark does NOT move is an assertion a command that does
// nothing at all satisfies — it passed against the TODO stub this branch
// replaced, which is the "test whose data never reaches the path" failure this
// project keeps hitting. The two together say the mark tracks whether the cycle
// finished, rather than never moving.
func TestSubagentStopAdvancesTheMarkOnACompletedCycle(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tree := initRepo(t)
	commitFile(t, tree, "seed.txt", "seed")

	p := subagentSession(t, tree)
	subID, err := stableID(p)
	require.NoError(t, err)
	dbPath, err := sessionDBPath(tree, subID)
	require.NoError(t, err)

	seed, err := sessionstate.Open(dbPath)
	require.NoError(t, err)
	require.NoError(t, seed.SetMeta(sessionstate.MetaTranscriptOffered, "some-turn"))
	require.NoError(t, seed.Close())

	original := natureStopDispatch
	t.Cleanup(func() { natureStopDispatch = original })
	natureStopDispatch = func(*cobra.Command, HookPayload) string { return "" }

	_, _, err = runSubagentStopWith(t, p)
	require.NoError(t, err)

	store, err := sessionstate.Open(dbPath)
	require.NoError(t, err)
	defer store.Close()

	mark, ok, err := store.Meta(sessionstate.MetaTranscriptRead)
	require.NoError(t, err)
	require.True(t, ok,
		"a sub-agent's finished cycle left no read mark — the next one re-reads turns it already judged")
	assert.Equal(t, "some-turn", mark)
}

// TestSubagentStopHoldsTheMarkWhenDispatchDidNotFinish: the read mark says a
// position has been judged, and a cycle whose dispatch was cut short has judged
// nothing. Advancing anyway would put the turns the sub-agent must correct
// behind the mark, where they are never offered again.
//
// Paired with the positive case above; on its own this passes against a command
// that does nothing.
func TestSubagentStopHoldsTheMarkWhenDispatchDidNotFinish(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tree := initRepo(t)
	commitFile(t, tree, "seed.txt", "seed")

	p := subagentSession(t, tree)
	subID, err := stableID(p)
	require.NoError(t, err)
	dbPath, err := sessionDBPath(tree, subID)
	require.NoError(t, err)

	// A position this cycle read out, as `session query` would have recorded it.
	seed, err := sessionstate.Open(dbPath)
	require.NoError(t, err)
	require.NoError(t, seed.SetMeta(sessionstate.MetaTranscriptOffered, "some-turn"))
	require.NoError(t, seed.Close())

	original := natureStopDispatch
	t.Cleanup(func() { natureStopDispatch = original })
	natureStopDispatch = func(*cobra.Command, HookPayload) string { return "a rule refused" }

	_, _, err = runSubagentStopWith(t, p)
	require.NoError(t, err)

	store, err := sessionstate.Open(dbPath)
	require.NoError(t, err)
	defer store.Close()

	_, ok, err := store.Meta(sessionstate.MetaTranscriptRead)
	require.NoError(t, err)
	assert.False(t, ok,
		"the mark advanced on a cycle a Stop refusal blocked — those turns are now behind it")
}
