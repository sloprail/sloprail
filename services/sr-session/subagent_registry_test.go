package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/sessionstate"
)

const regSession = "root-session"

var regT0 = time.Unix(1_800_000_000, 0)

type registryFixture struct {
	t     *testing.T
	dir   string
	home  string
	store sessionstate.Store
	now   time.Time
	errs  *bytes.Buffer
}

func newRegistryFixture(t *testing.T) *registryFixture {
	t.Helper()
	f := &registryFixture{t: t, dir: t.TempDir(), home: t.TempDir(), now: regT0, errs: &bytes.Buffer{}}
	s, err := sessionstate.Open(filepath.Join(f.dir, "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	f.store = s
	oldHome := agentHome
	agentHome = func() string { return f.home }
	t.Cleanup(func() { agentHome = oldHome })
	return f
}

func (f *registryFixture) config(body string) {
	f.t.Helper()
	require.NoError(f.t, os.MkdirAll(filepath.Join(f.dir, ".sloprail"), 0o755))
	require.NoError(f.t, os.WriteFile(filepath.Join(f.dir, ".sloprail", "config.yaml"), []byte(body), 0o644))
}

// transcript writes a dispatching record of the given lines and returns its path.
func (f *registryFixture) transcript(name string, lines ...string) string {
	f.t.Helper()
	path := filepath.Join(f.dir, name+".jsonl")
	require.NoError(f.t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	return path
}

func launchRecord(agent string) []string {
	return []string{
		`{"type":"assistant","uuid":"a-` + agent + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_` + agent + `","name":"Agent","input":{"prompt":"go","run_in_background":true}}]}}`,
		`{"type":"user","uuid":"r-` + agent + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_` + agent + `","content":"Async agent launched successfully.\nagentId: ` + agent + ` (internal ID)"}]}}`,
	}
}

func notificationRecord(agent, status string) string {
	return `{"type":"attachment","uuid":"n-` + agent + status + `","attachment":{"type":"queued_command","commandMode":"task-notification","prompt":"<task-notification>\n<task-id>` + agent + `</task-id>\n<status>` + status + `</status>\n</task-notification>"}}`
}

// settle runs the Stop's registry step at time f.now over the dispatching record at path.
func (f *registryFixture) settle(path string) agentPlan {
	f.t.Helper()
	cmd := &cobra.Command{}
	cmd.SetErr(f.errs)
	return settleAgents(cmd, f.store, regSession, HookPayload{TranscriptPath: path, Cwd: f.dir, SessionID: "harness-1"}, f.now)
}

func (f *registryFixture) agent(id string) sessionstate.Agent {
	f.t.Helper()
	as, err := f.store.Agents(regSession)
	require.NoError(f.t, err)
	for _, a := range as {
		if a.AgentID == id {
			return a
		}
	}
	f.t.Fatalf("agent %s not registered", id)
	return sessionstate.Agent{}
}

func (f *registryFixture) own(agent, folder string) {
	f.t.Helper()
	require.NoError(f.t, f.store.TrackRange(sessionstate.TrackedRange{SessionID: regSession, Folder: folder, Head: "feat-" + agent, Base: "b", AgentID: agent}))
}

// sr:proves subagents/running-background-agent-left-unjudged
func TestSettle_ABackgroundLaunchIsWaitedFor_AndItsNotificationEndsTheWait(t *testing.T) {
	f := newRegistryFixture(t)
	path := f.transcript("p", launchRecord("bg1")...)
	plan := f.settle(path)
	assert.True(t, plan.Waiting["bg1"])
	assert.Equal(t, sessionstate.AgentRunning, f.agent("bg1").Status)
	assert.True(t, f.agent("bg1").Background)

	path = f.transcript("p", append(launchRecord("bg1"), notificationRecord("bg1", "completed"))...)
	plan = f.settle(path)
	assert.False(t, plan.Waiting["bg1"])
	assert.Equal(t, sessionstate.AgentCompleted, f.agent("bg1").Status)
}

func TestSettle_EachTerminalNotificationStatusEndsTheRun(t *testing.T) {
	for _, status := range []string{"completed", "failed", "killed", "stopped"} {
		f := newRegistryFixture(t)
		f.settle(f.transcript("p", launchRecord("bg1")...))
		plan := f.settle(f.transcript("p", append(launchRecord("bg1"), notificationRecord("bg1", status))...))
		assert.False(t, plan.Waiting["bg1"], status)
		assert.Equal(t, status, f.agent("bg1").Status)
	}
	f := newRegistryFixture(t)
	plan := f.settle(f.transcript("p", append(launchRecord("bg1"), notificationRecord("bg1", "running"))...))
	assert.True(t, plan.Waiting["bg1"], "a non-terminal status ends nothing")
}

// An agent the registry has never heard of is judged, and so is one only a start hook announced:
// nothing says it is a background agent, so nothing is left unjudged for it.
// sr:proves subagents/ranges-verified-at-the-parents-turn-end
func TestSettle_AnUnknownAgentIsJudged_AndSoIsOneThatIsNotKnownToBeBackground(t *testing.T) {
	f := newRegistryFixture(t)
	plan := f.settle(f.transcript("p", "{}"))
	assert.Empty(t, plan.Waiting)

	require.NoError(t, f.store.StartAgent(sessionstate.AgentSignal{SessionID: regSession, AgentID: "fg", At: regT0}))
	plan = f.settle(f.transcript("p", "{}"))
	assert.False(t, plan.Waiting["fg"], "a start hook alone does not say the agent runs in the background")
	assert.Equal(t, sessionstate.AgentRunning, f.agent("fg").Status)
}

// Compaction rewrites what the transcript holds; a resume reads a new file. The registry is the
// store's, so the agent is still waited for, and its end is still heard.
// sr:proves subagents/agent-registry-survives-compaction
func TestSettle_CompactionAndAResumedSessionWithANewTranscriptKeepTheRegistry(t *testing.T) {
	f := newRegistryFixture(t)
	f.settle(f.transcript("before", launchRecord("bg1")...))

	// After a compaction: the launch is gone from the record.
	f.now = f.now.Add(time.Minute)
	plan := f.settle(f.transcript("compacted", `{"type":"summary","summary":"earlier work"}`))
	assert.True(t, plan.Waiting["bg1"], "a compaction must not make a running agent unknown")

	// Resumed into a brand new transcript file, with no record of the launch at all.
	f.now = f.now.Add(time.Minute)
	plan = f.settle(f.transcript("resumed", `{"type":"user","uuid":"u1","message":{"role":"user","content":"continue"}}`))
	assert.True(t, plan.Waiting["bg1"])
	assert.True(t, f.agent("bg1").Background)

	// A missing record changes nothing either.
	plan = f.settle(filepath.Join(f.dir, "gone.jsonl"))
	assert.True(t, plan.Waiting["bg1"])
	assert.Contains(t, f.errs.String(), "not read")

	// The terminal notification arriving in the new file ends it.
	plan = f.settle(f.transcript("resumed", notificationRecord("bg1", "completed")))
	assert.False(t, plan.Waiting["bg1"])
}

// The transcript read again, or a new file that repeats the old records, must not end a run that
// began after the notification.
func TestSettle_ANotificationIsAppliedOnce(t *testing.T) {
	f := newRegistryFixture(t)
	lines := append(launchRecord("bg1"), notificationRecord("bg1", "completed"))
	f.settle(f.transcript("p", lines...))
	assert.Equal(t, sessionstate.AgentCompleted, f.agent("bg1").Status)

	// The dispatcher resumed the agent: it runs again (SubagentStart).
	require.NoError(t, f.store.StartAgent(sessionstate.AgentSignal{SessionID: regSession, AgentID: "bg1", At: f.now}))
	plan := f.settle(f.transcript("p", lines...))
	assert.Equal(t, sessionstate.AgentRunning, f.agent("bg1").Status, "an old notification, read again, does not end the new run")
	assert.True(t, plan.Waiting["bg1"])
}

// sr:proves subagents/silent-agent-stops-being-waited-for
func TestSettle_ASilentAgentIsNamedThenEscalatedToStaleAndJudged(t *testing.T) {
	f := newRegistryFixture(t)
	f.own("bg1", "/w1")
	path := f.transcript("p", launchRecord("bg1")...)
	require.NoError(t, f.store.TouchAgent(sessionstate.AgentSignal{SessionID: regSession, AgentID: "bg1", At: regT0}))
	f.settle(path)

	f.now = regT0.Add(9 * time.Minute)
	plan := f.settle(path)
	assert.True(t, plan.Waiting["bg1"])
	assert.Empty(t, plan.Silent, "not silent yet")

	f.now = regT0.Add(15 * time.Minute)
	plan = f.settle(path)
	assert.True(t, plan.Waiting["bg1"], "silent but not stale: still unjudged, and said so")
	assert.Equal(t, "sub-agent bg1 has been silent for 15 min; its ranges are still unjudged", plan.Silent["bg1"])
	assert.Equal(t, sessionstate.AgentRunning, f.agent("bg1").Status)

	// Its own hook is a sign of life: the silence starts over.
	require.NoError(t, f.store.TouchAgent(sessionstate.AgentSignal{SessionID: regSession, AgentID: "bg1", At: f.now}))
	f.now = f.now.Add(5 * time.Minute)
	plan = f.settle(path)
	assert.Empty(t, plan.Silent)
	assert.True(t, plan.Waiting["bg1"])

	// Past the stale threshold it is no longer waited for: fail closed.
	f.now = f.now.Add(61 * time.Minute)
	plan = f.settle(path)
	assert.False(t, plan.Waiting["bg1"])
	assert.Equal(t, sessionstate.AgentStale, f.agent("bg1").Status)

	// Stale stays judged at the next Stop too, and a hook of its own brings it back.
	plan = f.settle(path)
	assert.False(t, plan.Waiting["bg1"])
	require.NoError(t, f.store.TouchAgent(sessionstate.AgentSignal{SessionID: regSession, AgentID: "bg1", At: f.now}))
	plan = f.settle(path)
	assert.True(t, plan.Waiting["bg1"], "an agent calling tools is running")
}

// sr:proves subagents/silent-agent-stops-being-waited-for
func TestSettle_TheAgentsOwnTranscriptGrowingIsActivity(t *testing.T) {
	f := newRegistryFixture(t)
	f.own("bg1", "/w1")
	own := filepath.Join(f.dir, "agent-bg1.jsonl")
	require.NoError(t, os.WriteFile(own, []byte("{}\n"), 0o644))
	f.now = time.Now() // its transcript was written just now
	require.NoError(t, f.store.StartAgent(sessionstate.AgentSignal{SessionID: regSession, AgentID: "bg1", TranscriptPath: own, At: f.now.Add(-2 * time.Hour)}))
	path := f.transcript("p", launchRecord("bg1")...)
	plan := f.settle(path)
	assert.True(t, plan.Waiting["bg1"], "a transcript written to moments ago is a live agent, whatever its hooks said")
	assert.Empty(t, plan.Silent)

	old := time.Now().Add(-30 * time.Minute)
	require.NoError(t, os.Chtimes(own, old, old))
	require.NoError(t, f.store.TouchAgent(sessionstate.AgentSignal{SessionID: regSession, AgentID: "bg1", At: old}))
	plan = f.settle(path)
	assert.Regexp(t, "silent for (29|30) min", plan.Silent["bg1"])
}

func TestSettle_ASilentAgentWithNoRangesIsNotNamed(t *testing.T) {
	f := newRegistryFixture(t)
	path := f.transcript("p", launchRecord("bg1")...)
	f.settle(path)
	f.now = regT0.Add(20 * time.Minute)
	plan := f.settle(path)
	assert.Empty(t, plan.Silent, "there are no ranges left unjudged to name")
}

// sr:proves subagents/silent-agent-stops-being-waited-for
func TestSettle_ThresholdsComeFromTheConfig(t *testing.T) {
	f := newRegistryFixture(t)
	f.config("subagent_silent_after_minutes: 2\nsubagent_stale_after_minutes: 4\n")
	f.own("bg1", "/w1")
	path := f.transcript("p", launchRecord("bg1")...)
	f.settle(path)
	f.now = regT0.Add(3 * time.Minute)
	plan := f.settle(path)
	assert.Contains(t, plan.Silent["bg1"], "silent for 3 min")
	f.now = regT0.Add(5 * time.Minute)
	plan = f.settle(path)
	assert.False(t, plan.Waiting["bg1"])
	assert.Equal(t, sessionstate.AgentStale, f.agent("bg1").Status)
}

func TestSettle_AnInvalidConfigFallsBackToTheDefaultsAndSaysSo(t *testing.T) {
	f := newRegistryFixture(t)
	f.config("subagent_silent_after_minutes: 50\nsubagent_stale_after_minutes: 20\n")
	path := f.transcript("p", launchRecord("bg1")...)
	f.settle(path)
	f.now = regT0.Add(30 * time.Minute)
	plan := f.settle(path)
	assert.True(t, plan.Waiting["bg1"], "the defaults apply: 30 min is below stale")
	assert.Contains(t, f.errs.String(), "subagent_silent_after_minutes")
}

// The session's process is gone (nothing can still be running under it): stale, judged.
// sr:proves subagents/ranges-verified-at-the-parents-turn-end
func TestSettle_AnAgentWhoseHarnessProcessIsGoneIsStale(t *testing.T) {
	f := newRegistryFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(f.home, ".claude", "sessions"), 0o755))
	require.NoError(t, f.store.NoteAgentLaunch(sessionstate.AgentSignal{SessionID: regSession, AgentID: "bg1", OwnerPID: 999999, OwnerProcStart: "old", At: regT0}))
	plan := f.settle(f.transcript("p", "{}"))
	assert.False(t, plan.Waiting["bg1"])
	assert.Equal(t, sessionstate.AgentStale, f.agent("bg1").Status)

	// Without a sessions directory nothing is known about the process: the clock decides.
	f2 := newRegistryFixture(t)
	require.NoError(t, f2.store.NoteAgentLaunch(sessionstate.AgentSignal{SessionID: regSession, AgentID: "bg1", OwnerPID: 999999, OwnerProcStart: "old", At: regT0}))
	assert.True(t, f2.settle(f2.transcript("p", "{}")).Waiting["bg1"])
}

func TestSettle_AnAgentOfTheLiveProcessIsNotStale(t *testing.T) {
	f := newRegistryFixture(t)
	pid := os.Getpid()
	body := `{"pid":` + itoaInt(pid) + `,"sessionId":"harness-1","procStart":"s"}`
	require.NoError(t, os.MkdirAll(filepath.Join(f.home, ".claude", "sessions"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.home, ".claude", "sessions", itoaInt(pid)+".json"), []byte(body), 0o644))
	require.NoError(t, f.store.NoteAgentLaunch(sessionstate.AgentSignal{SessionID: regSession, AgentID: "bg1", OwnerPID: pid, OwnerProcStart: "s", At: regT0}))
	assert.True(t, f.settle(f.transcript("p", "{}")).Waiting["bg1"])
}

func itoaInt(n int) string { return strconv.Itoa(n) }
