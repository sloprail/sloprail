package sessionstate

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Unix(1_800_000_000, 0)

func sig(agent string, at time.Time) AgentSignal {
	return AgentSignal{SessionID: "s", AgentID: agent, At: at}
}

func statusOf(t *testing.T, s Store, agent string) Agent {
	t.Helper()
	as, err := s.Agents("s")
	require.NoError(t, err)
	for _, a := range as {
		if a.AgentID == agent {
			return a
		}
	}
	t.Fatalf("agent %s not in the registry", agent)
	return Agent{}
}

func TestAgents_FoldersAreRecordedOncePerAgentAndListed(t *testing.T) {
	s := openTemp(t)
	require.NoError(t, s.StartAgent(sig("a1", t0)))
	require.NoError(t, s.StartAgent(sig("a2", t0)))
	require.NoError(t, s.NoteAgentFolder("s", "a1", "/r/two"))
	require.NoError(t, s.NoteAgentFolder("s", "a1", "/r/one"))
	require.NoError(t, s.NoteAgentFolder("s", "a1", "/r/one"))
	assert.Equal(t, []string{"/r/one", "/r/two"}, statusOf(t, s, "a1").Folders)
	assert.Empty(t, statusOf(t, s, "a2").Folders)
	assert.Error(t, s.NoteAgentFolder("s", "", "/r"))
}

func TestAgents_StartMakesARunningAgentAndRecordsItsFacts(t *testing.T) {
	s := openTemp(t)
	require.NoError(t, s.StartAgent(AgentSignal{SessionID: "s", AgentID: "a1", AgentType: "Explore", TranscriptPath: "/t/a1.jsonl", OwnerPID: 7, OwnerProcStart: "p", At: t0}))
	a := statusOf(t, s, "a1")
	assert.Equal(t, AgentRunning, a.Status)
	assert.Equal(t, "Explore", a.AgentType)
	assert.Equal(t, "/t/a1.jsonl", a.TranscriptPath)
	assert.Equal(t, t0.Unix(), a.StartedAt.Unix())
	assert.Equal(t, t0.Unix(), a.LastSeenAt.Unix())
	assert.True(t, a.EndedAt.IsZero())
	assert.False(t, a.Background, "a start hook does not say the agent is a background one")
	assert.Equal(t, 7, a.OwnerPID)
}

func TestAgents_EveryTransition(t *testing.T) {
	s := openTemp(t)
	later := t0.Add(time.Minute)

	// running -> each terminal status; the first verdict stands.
	for _, status := range []string{AgentCompleted, AgentFailed, AgentKilled, AgentStopped} {
		id := "to-" + status
		require.NoError(t, s.StartAgent(sig(id, t0)))
		require.NoError(t, s.EndAgent("s", id, status, later))
		a := statusOf(t, s, id)
		assert.Equal(t, status, a.Status)
		assert.Equal(t, later.Unix(), a.EndedAt.Unix())
		require.NoError(t, s.EndAgent("s", id, AgentFailed, later.Add(time.Hour)))
		require.NoError(t, s.EndAgent("s", id, AgentCompleted, later.Add(time.Hour)))
		if status != AgentFailed {
			assert.Equal(t, status, statusOf(t, s, id).Status, "a later verdict does not rewrite the first")
		}
	}
	require.Error(t, s.EndAgent("s", "x", AgentStale, later), "stale is not a harness verdict")
	require.Error(t, s.EndAgent("s", "x", AgentRunning, later))

	// running -> stale -> a verdict, and stale -> running when the agent calls a tool.
	require.NoError(t, s.StartAgent(sig("st", t0)))
	require.NoError(t, s.MarkAgentStale("s", "st", later))
	assert.Equal(t, AgentStale, statusOf(t, s, "st").Status)
	require.NoError(t, s.EndAgent("s", "st", AgentKilled, later))
	assert.Equal(t, AgentKilled, statusOf(t, s, "st").Status, "a stale agent still takes the harness's verdict")
	require.NoError(t, s.StartAgent(sig("st2", t0)))
	require.NoError(t, s.MarkAgentStale("s", "st2", later))
	require.NoError(t, s.TouchAgent(sig("st2", later.Add(time.Minute))))
	a := statusOf(t, s, "st2")
	assert.Equal(t, AgentRunning, a.Status, "an agent calling tools is running")
	assert.True(t, a.EndedAt.IsZero())

	// stale only ever comes from running.
	require.NoError(t, s.StartAgent(sig("done", t0)))
	require.NoError(t, s.EndAgent("s", "done", AgentCompleted, later))
	require.NoError(t, s.MarkAgentStale("s", "done", later))
	assert.Equal(t, AgentCompleted, statusOf(t, s, "done").Status)

	// completed -> running by a tool call (a Stop that blocked sent it round again) or a new start;
	// failed/killed/stopped are not undone by a tool call, but a new start runs it again.
	require.NoError(t, s.TouchAgent(sig("done", later)))
	assert.Equal(t, AgentRunning, statusOf(t, s, "done").Status)
	require.NoError(t, s.EndAgent("s", "to-"+AgentKilled, AgentKilled, later))
	require.NoError(t, s.TouchAgent(sig("to-"+AgentKilled, later)))
	assert.Equal(t, AgentKilled, statusOf(t, s, "to-"+AgentKilled).Status)
	require.NoError(t, s.StartAgent(sig("to-"+AgentKilled, later)))
	assert.Equal(t, AgentRunning, statusOf(t, s, "to-"+AgentKilled).Status, "a resumed agent runs again")
}

func TestAgents_AToolCallOfAnUnknownAgentRegistersItRunning_AndOnlyLastSeenMoves(t *testing.T) {
	s := openTemp(t)
	require.NoError(t, s.TouchAgent(AgentSignal{SessionID: "s", AgentID: "u", TranscriptPath: "/t/u.jsonl", At: t0}))
	a := statusOf(t, s, "u")
	assert.Equal(t, AgentRunning, a.Status)
	assert.False(t, a.Background)
	require.NoError(t, s.TouchAgent(sig("u", t0.Add(5*time.Minute))))
	a = statusOf(t, s, "u")
	assert.Equal(t, t0.Unix(), a.StartedAt.Unix())
	assert.Equal(t, t0.Add(5*time.Minute).Unix(), a.LastSeenAt.Unix())
	assert.Equal(t, "/t/u.jsonl", a.TranscriptPath, "a touch without a path keeps the path")
}

func TestAgents_ALaunchSeenInTheRecordMarksBackgroundAndNeverRevivesAFinishedAgent(t *testing.T) {
	s := openTemp(t)
	require.NoError(t, s.NoteAgentLaunch(sig("bg", t0)))
	a := statusOf(t, s, "bg")
	assert.True(t, a.Background)
	assert.Equal(t, AgentRunning, a.Status)
	require.NoError(t, s.EndAgent("s", "bg", AgentCompleted, t0.Add(time.Minute)))
	require.NoError(t, s.NoteAgentLaunch(sig("bg", t0.Add(time.Hour)))) // the record read again
	assert.Equal(t, AgentCompleted, statusOf(t, s, "bg").Status, "a launch read again must not bring a finished agent back")

	// A start hook seen first, the launch later: the agent gains the mark, keeps its status.
	require.NoError(t, s.StartAgent(sig("h", t0)))
	require.NoError(t, s.NoteAgentLaunch(sig("h", t0)))
	assert.True(t, statusOf(t, s, "h").Background)
}

func TestAgents_ASignalIsApplicableOnce(t *testing.T) {
	s := openTemp(t)
	fresh, err := s.NewAgentSignal("s", "u1:a")
	require.NoError(t, err)
	assert.True(t, fresh)
	fresh, err = s.NewAgentSignal("s", "u1:a")
	require.NoError(t, err)
	assert.False(t, fresh)
	fresh, err = s.NewAgentSignal("other", "u1:a")
	require.NoError(t, err)
	assert.True(t, fresh, "signals are per session")
}

func TestAgents_ListCarriesTheTrackedRangesTheAgentOwns(t *testing.T) {
	s := openTemp(t)
	require.NoError(t, s.StartAgent(sig("a1", t0)))
	require.NoError(t, s.StartAgent(sig("a2", t0.Add(time.Second))))
	require.NoError(t, s.TrackRange(TrackedRange{SessionID: "s", Folder: "/w1", Head: "feat", Base: "b", AgentID: "a1"}))
	require.NoError(t, s.TrackRange(TrackedRange{SessionID: "s", Folder: "/w2", Head: "x", Base: "b", AgentID: "a2"}))
	require.NoError(t, s.UntrackRange("s", "/w2", "x", "dropped", "a2", "t"))
	require.NoError(t, s.TrackRange(TrackedRange{SessionID: "s", Folder: "/r", Head: "main", Base: "b"}))
	as, err := s.Agents("s")
	require.NoError(t, err)
	require.Len(t, as, 2)
	require.Len(t, as[0].Ranges, 1)
	assert.Equal(t, "feat", as[0].Ranges[0].Head)
	assert.Empty(t, as[1].Ranges, "an untracked range is no longer the agent's to answer for")
}

// The registry belongs to the session's own store, not to a transcript or a harness session id:
// closed and opened again (a resume in a new process), it is all there.
// sr:proves subagents/agent-registry-survives-compaction
func TestAgents_SurviveAReopenedStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, s.NoteAgentLaunch(sig("bg", t0)))
	require.NoError(t, s.Close())
	s, err = Open(path)
	require.NoError(t, err)
	defer s.Close()
	a := statusOf(t, s, "bg")
	assert.True(t, a.Background)
	assert.Equal(t, AgentRunning, a.Status)
}

// A store from before the sub-agent registry (user_version 6: session_refs is the tracked-ranges
// table, no session_agents) is migrated by the engine on open: what it holds stays, and the
// registry starts empty, so an agent the older engine never recorded is "unknown" and judged.
func TestAgents_AStoreFromBeforeTheRegistryIsMigratedAndItsRowsStay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	old, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	files, err := migrationFiles()
	require.NoError(t, err)
	require.Greater(t, len(files), 6)
	require.Equal(t, "007_session_agents.sql", files[6])
	for _, f := range files[:6] {
		body, err := migrationFS.ReadFile("migrations/" + f)
		require.NoError(t, err)
		_, err = old.Exec(string(body))
		require.NoError(t, err, f)
	}
	_, err = old.Exec(`PRAGMA user_version = 6`)
	require.NoError(t, err)
	_, err = old.Exec(`INSERT INTO session_refs (session_id, folder, ref, first_tip, tip, agent_id, base, added_by, untracked_reason) VALUES
		('s', '/w', 'feat', 'a1', 'a2', 'sub1', 'b0', 'auto', ''),
		('s', '/r', 'main', 'c1', 'c2', '', 'b0', 'auto', '')`)
	require.NoError(t, err)
	_, err = old.Exec(`INSERT INTO meta (key, value) VALUES ('k', 'v')`)
	require.NoError(t, err)
	require.NoError(t, old.Close())

	s, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	v, had, err := s.Meta("k")
	require.NoError(t, err)
	assert.True(t, had)
	assert.Equal(t, "v", v)
	as, err := s.Agents("s")
	require.NoError(t, err)
	assert.Empty(t, as, "the registry starts empty: an agent the older engine never recorded is unknown")
	rs, err := s.Ranges("s")
	require.NoError(t, err)
	require.Len(t, rs, 2)
	owners := map[string]string{rs[0].Head: rs[0].AgentID, rs[1].Head: rs[1].AgentID}
	assert.Equal(t, map[string]string{"feat": "sub1", "main": ""}, owners)

	// And the registry works on the migrated store, joining the rows that were already there.
	require.NoError(t, s.StartAgent(sig("sub1", t0)))
	as, err = s.Agents("s")
	require.NoError(t, err)
	require.Len(t, as, 1)
	require.Len(t, as[0].Ranges, 1)
	assert.Equal(t, "feat", as[0].Ranges[0].Head)

	// Opened again, nothing is migrated twice.
	require.NoError(t, s.Close())
	s2, err := Open(path)
	require.NoError(t, err)
	defer s2.Close()
	as, err = s2.Agents("s")
	require.NoError(t, err)
	assert.Len(t, as, 1)
}

func TestAgents_ARunNeedsASessionAndAnAgent(t *testing.T) {
	s := openTemp(t)
	require.Error(t, s.StartAgent(AgentSignal{AgentID: "a"}))
	require.Error(t, s.TouchAgent(AgentSignal{SessionID: "s"}))
	require.Error(t, s.NoteAgentLaunch(AgentSignal{}))
}
