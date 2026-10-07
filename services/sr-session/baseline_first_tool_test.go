package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// The point is taken before the session's first tool call, because session
// start cannot take it: Claude Code writes a fresh session's transcript AFTER
// SessionStart's hooks have run, so the identity the store is keyed by does not
// exist there yet. Left to the first Stop, the point landed after the agent's
// first turn — on its own final commit when it committed that turn — and the
// difference that should have held its change came back empty. See
// ensureBaselineRecorded.

// TestEnsureBaselineRecorded_TakesThePointWhenThereIsNone is the first tool
// call of a fresh session.
func TestEnsureBaselineRecorded_TakesThePointWhenThereIsNone(t *testing.T) {
	dir := initRepo(t)
	want := commitFile(t, dir, "a.txt", "one")
	s := openStore(t)

	outcome, err := ensureBaselineRecorded(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineRecorded, outcome)

	commit, branch := baseline(t, s)
	assert.Equal(t, want, commit)
	assert.Equal(t, "main", branch)
}

// TestEnsureBaselineRecorded_DoesNotFollowTheAgentsCommit is the bug itself, at
// the level of the rule: once the first tool call has recorded the point, the
// agent's commit later in the same turn must not move it — and neither may any
// later tool call.
func TestEnsureBaselineRecorded_DoesNotFollowTheAgentsCommit(t *testing.T) {
	dir := initRepo(t)
	start := commitFile(t, dir, "a.txt", "one")
	s := openStore(t)

	_, err := ensureBaselineRecorded(s, dir)
	require.NoError(t, err)

	agentCommit := commitFile(t, dir, "charge.go", "package src")
	require.NotEqual(t, start, agentCommit)

	outcome, err := ensureBaselineRecorded(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineUnchanged, outcome)

	commit, _ := baseline(t, s)
	assert.Equal(t, start, commit, "the point must stay where the session began, not on the agent's commit")
}

// TestEnsureBaselineRecorded_LeavesMovingToTheEndOfTheCycle: a tool call only
// ever takes a missing point. Noticing the tree left the recorded history is
// the end of the cycle's job (ensureBaseline at Stop), where the difference is
// measured and the move is announced — so after a switch to a divergent line a
// tool call leaves the point alone, and Stop still moves it.
func TestEnsureBaselineRecorded_LeavesMovingToTheEndOfTheCycle(t *testing.T) {
	dir := initRepo(t)
	commitFile(t, dir, "a.txt", "one")
	// A second commit, so the branch divergentBranch cuts off the root does not
	// contain the recorded point.
	start := commitFile(t, dir, "b.txt", "two")
	featureTip := divergentBranch(t, dir, "feature")
	s := openStore(t)

	_, err := ensureBaselineRecorded(s, dir)
	require.NoError(t, err)
	runGit(t, dir, "checkout", "feature")

	outcome, err := ensureBaselineRecorded(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineUnchanged, outcome)
	commit, _ := baseline(t, s)
	assert.Equal(t, start, commit, "a tool call moved the point; that is Stop's decision")

	outcome, err = ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineMoved, outcome, "Stop must still re-take the point after a branch switch")
	commit, _ = baseline(t, s)
	assert.Equal(t, featureTip, commit)
}

// TestEnsureBaselineRecorded_HalfWrittenPointIsRetaken: a commit with no branch
// is not a point (see writeBaseline), so the tool call takes a whole one.
func TestEnsureBaselineRecorded_HalfWrittenPointIsRetaken(t *testing.T) {
	dir := initRepo(t)
	want := commitFile(t, dir, "a.txt", "one")
	s := openStore(t)
	require.NoError(t, s.SetMeta(sessionstate.MetaBaselineCommit, "stale-commit"))

	outcome, err := ensureBaselineRecorded(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineRecorded, outcome)
	commit, branch := baseline(t, s)
	assert.Equal(t, want, commit)
	assert.Equal(t, "main", branch)
}

// TestEnsureBaselineRecorded_AsksNoGitOnceRecorded pins the cost claim: with a
// point recorded, the per-tool-call path is two store reads and no git at all.
// Shown by pointing it at a directory that is not a repository — ensureBaseline
// would ask git and answer baselineUnavailable; this answers from the store.
func TestEnsureBaselineRecorded_AsksNoGitOnceRecorded(t *testing.T) {
	s := openStore(t)
	require.NoError(t, s.SetMeta(sessionstate.MetaBaselineCommit, "c0ffee"))
	require.NoError(t, s.SetMeta(sessionstate.MetaBaselineBranch, "main"))

	outcome, err := ensureBaselineRecorded(s, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, baselineUnchanged, outcome)
}

// runHook drives one hook command with a payload on stdin, as a harness does.
func runHook(t *testing.T, cmd *cobra.Command, p HookPayload) (stdout, stderr string) {
	t.Helper()
	body, err := json.Marshal(p)
	require.NoError(t, err)
	var out, errOut bytes.Buffer
	cmd.SetIn(bytes.NewReader(body))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)
	require.NoError(t, cmd.Execute())
	return out.String(), errOut.String()
}

// readBaseline opens the store a payload keys to and reads its point back.
func readBaseline(t *testing.T, p HookPayload) (commit string, ok bool) {
	t.Helper()
	id, err := stableID(p)
	require.NoError(t, err)
	path, err := sessionDBPath(p.Cwd, id)
	require.NoError(t, err)
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return "", false
	}
	s, err := sessionstate.Open(path)
	require.NoError(t, err)
	defer s.Close()
	commit, ok, err = s.Meta(sessionstate.MetaBaselineCommit)
	require.NoError(t, err)
	return commit, ok
}

// TestSessionStart_UnwrittenRecordIsQuiet: a fresh session's SessionStart names
// a transcript that does not exist yet. That is the harness working as designed
// — it writes the file after SessionStart — so it is not reported as a failure
// to the person watching the session start.
func TestSessionStart_UnwrittenRecordIsQuiet(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tree := initRepo(t)
	commitFile(t, tree, "seed.txt", "seed")
	p := HookPayload{
		TranscriptPath: filepath.Join(t.TempDir(), "fresh-session.jsonl"),
		SessionID:      "fresh-session",
		Cwd:            tree,
	}

	_, stderr := runHook(t, newSessionStartCmd(), p)
	assert.NotContains(t, stderr, "no baseline recorded",
		"a transcript not yet written at SessionStart is expected, not a fault")
}

// TestSessionStart_BrokenIdentityIsStillReported: only the record's OWN absence
// is quiet. A record that exists and cannot be resolved is a broken identity,
// and silencing it would hide a session keyed nowhere.
func TestSessionStart_BrokenIdentityIsStillReported(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tree := initRepo(t)
	commitFile(t, tree, "seed.txt", "seed")
	path := filepath.Join(t.TempDir(), "no-origin.jsonl")
	// Every record has a parent: no origin to key on at all.
	require.NoError(t, os.WriteFile(path, []byte(
		`{"type":"user","uuid":"u1","parentUuid":"not-here"}`+"\n"), 0o644))

	_, stderr := runHook(t, newSessionStartCmd(), HookPayload{TranscriptPath: path, Cwd: tree})
	assert.Contains(t, stderr, "no baseline recorded")
}

// TestSessionStart_DegradedIdentityIsKeyedAndReported: a continuation whose
// earlier transcript is gone is not a broken identity any more. It keys on its
// own continuation root, records its baseline there, and says so once.
func TestSessionStart_DegradedIdentityIsKeyedAndReported(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tree := initRepo(t)
	start := commitFile(t, tree, "seed.txt", "seed")
	path := filepath.Join(t.TempDir(), "orphaned.jsonl")
	// A continuation whose earlier transcript is nowhere to be found.
	require.NoError(t, os.WriteFile(path, []byte(
		`{"type":"user","uuid":"u1","parentUuid":null,"logicalParentUuid":"gone"}`+"\n"), 0o644))
	p := HookPayload{TranscriptPath: path, Cwd: tree}

	_, stderr := runHook(t, newSessionStartCmd(), p)
	assert.NotContains(t, stderr, "no baseline recorded")
	assert.Contains(t, stderr, "earlier transcript is gone")

	store, err := openEngineState(p)
	require.NoError(t, err)
	defer store.Close()
	got, _, err := store.Meta(sessionstate.MetaBaselineCommit)
	require.NoError(t, err)
	assert.Equal(t, start, got, "the degraded session must still measure from where it started")

	_, again := runHook(t, newSessionStartCmd(), p)
	assert.NotContains(t, again, "earlier transcript is gone", "the degradation is reported once, not on every hook")
}

// TestPreTool_FirstToolCallTakesThePoint is the whole fix at the hook level: a
// session whose SessionStart could record nothing has its point taken by the
// first tool call — before the tool runs, so on the commit the session began on
// — and a commit the agent makes afterwards does not move it.
// sr:proves session/baseline-moves-only-on-leaving-history
func TestPreTool_FirstToolCallTakesThePoint(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tree := initRepo(t)
	start := commitFile(t, tree, "seed.txt", "seed")
	recordPath := filepath.Join(t.TempDir(), "fresh-session.jsonl")
	p := HookPayload{TranscriptPath: recordPath, SessionID: "fresh-session", Cwd: tree}

	// SessionStart: the record is not written yet, so nothing can be kept.
	runHook(t, newSessionStartCmd(), p)

	// The harness writes the record, then the agent asks for its first tool.
	require.NoError(t, os.WriteFile(recordPath, []byte(
		`{"type":"user","uuid":"origin","parentUuid":null,"message":{"role":"user","content":"fix it"}}`+"\n"), 0o644))
	pre := p
	pre.ToolName = "Bash"
	pre.ToolInput = json.RawMessage(`{"command":"git commit -am fix"}`)
	_, stderr := runHook(t, newSessionPreToolCmd(), pre)
	assert.NotContains(t, stderr, "no baseline recorded")

	commit, ok := readBaseline(t, p)
	require.True(t, ok, "the first tool call recorded no point; the first Stop would take it after the agent's turn")
	assert.Equal(t, start, commit)

	// The tool runs: the agent commits its work. The next tool call must not
	// follow it.
	commitFile(t, tree, "charge.go", "package src")
	runHook(t, newSessionPreToolCmd(), pre)
	commit, _ = readBaseline(t, p)
	assert.Equal(t, start, commit, "a later tool call moved the point onto the agent's own commit")
}

// TestPreTool_SubagentsFirstToolCallTakesItsOwnPoint: Claude Code reports
// agent_id on a sub-agent's tool events, so its PreToolUse resolves the
// sub-agent's own record and store. Its first tool call takes the point there —
// not in the parent's store, and not only at SubagentStop, which comes after
// the one cycle most sub-agents have.
func TestPreTool_SubagentsFirstToolCallTakesItsOwnPoint(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tree := initRepo(t)
	start := commitFile(t, tree, "seed.txt", "seed")

	sub := subagentSession(t, tree)
	// What a sub-agent's PreToolUse carries: the parent's transcript_path and the
	// agent's id, but no agent_transcript_path (that is SubagentStop's).
	pre := HookPayload{
		TranscriptPath: sub.TranscriptPath,
		AgentID:        sub.AgentID,
		Cwd:            tree,
		ToolName:       "Bash",
		ToolInput:      json.RawMessage(`{"command":"git commit -am work"}`),
	}
	path, err := recordOf(pre)
	require.NoError(t, err)
	require.Equal(t, sub.AgentTranscriptPath, path,
		"the Pre payload does not resolve to the sub-agent's record, so this proves nothing")
	require.Equal(t, filepath.Join(filepath.Dir(sub.TranscriptPath), "parent-session", transcript.SubagentDir, "agent-abc.jsonl"), path)

	runHook(t, newSessionPreToolCmd(), pre)

	commit, ok := readBaseline(t, sub)
	require.True(t, ok, "the sub-agent's first tool call recorded no point in its own store")
	assert.Equal(t, start, commit)

	_, parentOK := readBaseline(t, HookPayload{TranscriptPath: sub.TranscriptPath, Cwd: tree})
	assert.False(t, parentOK, "the sub-agent's tool call wrote a point into the dispatching session's store")

	// The sub-agent commits and ends its cycle: SubagentStop measures from where
	// it began, not from its own commit.
	commitFile(t, tree, "work.go", "package src")
	_, _, err = runSubagentStopWith(t, sub)
	require.NoError(t, err)
	commit, _ = readBaseline(t, sub)
	assert.Equal(t, start, commit, "SubagentStop moved the sub-agent's point onto its own commit")
}
