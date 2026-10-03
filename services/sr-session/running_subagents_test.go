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

	"github.com/sloprail/sloprail/internal/checkrun"
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

const rsOrigin = `{"type":"user","uuid":"origin","parentUuid":null,"message":{"role":"user","content":"go"}}`

func rsLaunch(agent string) []string {
	return []string{
		`{"type":"assistant","uuid":"a-` + agent + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"t-` + agent + `","name":"Agent","input":{"prompt":"go","run_in_background":true}}]}}`,
		`{"type":"user","uuid":"r-` + agent + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t-` + agent + `","content":"Async agent launched successfully.\nagentId: ` + agent + ` (internal ID)"}]}}`,
	}
}

func rsNotification(agent, status string) string {
	prompt, _ := json.Marshal("<task-notification>\n<task-id>" + agent + "</task-id>\n<status>" + status + "</status>\n</task-notification>")
	return `{"type":"attachment","uuid":"n-` + agent + status + `","attachment":{"type":"queued_command","commandMode":"task-notification","prompt":` + string(prompt) + `}}`
}

func rsWrite(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sess-rs.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	return path
}

// runningOf is the agents the Stop would leave unjudged, through the registry: the dispatching
// record is one input of it (settleRootAgents), over a store that starts empty.
func runningOf(t *testing.T, p HookPayload, ranges []sessionstate.TrackedRange) map[string]bool {
	t.Helper()
	store, err := sessionstate.Open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})
	return settleRootAgents(cmd, store, "root", p, ranges).Waiting
}

func TestRunningSubagents(t *testing.T) {
	ranges := []sessionstate.TrackedRange{{AgentID: "bg1"}}
	cases := []struct {
		name  string
		lines []string
		want  map[string]bool
	}{
		{"started without a notification", append([]string{rsOrigin}, rsLaunch("bg1")...), map[string]bool{"bg1": true}},
		{"completed", append(append([]string{rsOrigin}, rsLaunch("bg1")...), rsNotification("bg1", "completed")), map[string]bool{}},
		{"failed", append(append([]string{rsOrigin}, rsLaunch("bg1")...), rsNotification("bg1", "failed")), map[string]bool{}},
		{"killed", append(append([]string{rsOrigin}, rsLaunch("bg1")...), rsNotification("bg1", "killed")), map[string]bool{}},
		{"a notification-looking tool_result", append(append([]string{rsOrigin}, rsLaunch("bg1")...),
			`{"type":"user","uuid":"x","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"nope","content":"<task-notification><task-id>bg1</task-id><status>completed</status></task-notification>"}]}}`),
			map[string]bool{"bg1": true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := runningOf(t, HookPayload{TranscriptPath: rsWrite(t, c.lines...)}, ranges)
			if len(c.want) == 0 {
				assert.Empty(t, got)
			} else {
				assert.Equal(t, c.want, got)
			}
		})
	}
	t.Run("unreadable transcript: an unknown agent is judged", func(t *testing.T) {
		assert.Empty(t, runningOf(t, HookPayload{TranscriptPath: filepath.Join(t.TempDir(), "missing.jsonl")}, ranges))
	})
	t.Run("unparseable transcript: an unknown agent is judged", func(t *testing.T) {
		assert.Empty(t, runningOf(t, HookPayload{TranscriptPath: rsWrite(t, append(append([]string{rsOrigin}, rsLaunch("bg1")...), "{torn")...)}, ranges))
	})
	t.Run("a sub-agent's own Stop skips nothing", func(t *testing.T) {
		path := rsWrite(t, append([]string{rsOrigin}, rsLaunch("bg1")...)...)
		assert.Empty(t, runningOf(t, HookPayload{TranscriptPath: path, AgentID: "bg1"}, ranges))
	})
	t.Run("no row names an agent and the registry knows none: the record is not read", func(t *testing.T) {
		assert.Empty(t, runningOf(t, HookPayload{TranscriptPath: rsWrite(t, append([]string{rsOrigin}, rsLaunch("bg1")...)...)}, []sessionstate.TrackedRange{{}}))
	})
}

// stageStop sets up a session whose registry tracks three ranges of one repository, none judged:
// feature-bg (agent bg1), feature-fg (agent bg2) and feature-root (no agent). The session's record is lines.
func stageStop(t *testing.T, lines ...string) (HookPayload, string) {
	t.Helper()
	proj := initRepo(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	require.NoError(t, os.WriteFile(filepath.Join(proj, "README"), []byte("r"), 0o644))
	runGit(t, proj, "add", "README")
	runGit(t, proj, "commit", "-m", "init")
	writeFileGuardYAML(t, proj, "g", "match: path == \"x.md\"\nchecks:\n  - script: ./c.sh\n",
		map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	base := runGit(t, proj, "rev-parse", "HEAD")
	tips := map[string]string{}
	for _, b := range []string{"feature-bg", "feature-fg", "feature-root"} {
		runGit(t, proj, "switch", "-c", b, base)
		require.NoError(t, os.WriteFile(filepath.Join(proj, "x.md"), []byte(b), 0o644))
		runGit(t, proj, "add", "x.md")
		runGit(t, proj, "commit", "-m", b)
		tips[b] = runGit(t, proj, "rev-parse", "HEAD")
	}
	runGit(t, proj, "switch", "main")

	p := HookPayload{Cwd: proj, SessionID: "sess-rs", TranscriptPath: rsWrite(t, lines...)}
	rs, err := resolveRootSession(p)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(rs.Path), 0o755))
	root, err := sessionstate.Open(rs.Path)
	require.NoError(t, err)
	defer root.Close()
	for b, agent := range map[string]string{"feature-bg": "bg1", "feature-fg": "bg2", "feature-root": ""} {
		require.NoError(t, root.TrackRange(sessionstate.TrackedRange{SessionID: rs.ID, Folder: proj, Head: b, HeadSHA: tips[b], Base: base, AgentID: agent, AddedBy: sessionstate.RangeAgent}))
	}
	return p, proj
}

func stopOver(t *testing.T, p HookPayload) (refusals []string, notice string) {
	t.Helper()
	reg, err := modules.Registry()
	require.NoError(t, err)
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	n := withStopNotices(cmd)
	return verifyTrackedRanges(cmd, p, reg, nil), n.text()
}

func joined(refusals []string) string { return strings.Join(refusals, "\n") }

// A range of a still-running background agent is skipped and listed, never refused for; the
// ranges of a finished agent and of no agent are judged.
func TestVerifyTrackedRanges_ARunningAgentsRangeIsSkippedAndListed(t *testing.T) {
	lines := append([]string{rsOrigin}, rsLaunch("bg1")...)
	lines = append(lines, rsLaunch("bg2")...)
	lines = append(lines, rsNotification("bg2", "completed"))
	p, _ := stageStop(t, lines...)
	refusals, notice := stopOver(t, p)
	all := joined(refusals)
	assert.NotContains(t, all, "feature-bg", "the running agent's range was judged")
	assert.Contains(t, all, "feature-fg", "the finished agent's range was not judged")
	assert.Contains(t, all, "feature-root", "a row with no agent_id must always be judged")
	assert.Equal(t, 1, strings.Count(notice, "not judged yet: sub-agent bg1 still running"), notice)
	assert.NotContains(t, notice, "bg2")
}

// Once the agent's terminal notification is in the record, its range is judged.
func TestVerifyTrackedRanges_AFinishedAgentsRangeIsJudgedAtTheNextStop(t *testing.T) {
	lines := append([]string{rsOrigin}, rsLaunch("bg1")...)
	p, _ := stageStop(t, lines...)
	refusals, notice := stopOver(t, p)
	assert.NotContains(t, joined(refusals), "feature-bg")
	assert.Contains(t, notice, "bg1")

	f, err := os.OpenFile(p.TranscriptPath, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(rsNotification("bg1", "killed") + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	refusals, notice = stopOver(t, p)
	assert.Contains(t, joined(refusals), "feature-bg", "judged at the first Stop after the terminal notification")
	assert.Empty(t, notice)
}

// Fail closed: a record that cannot be read or parsed skips nothing.
func TestVerifyTrackedRanges_AnUnknownRecordJudgesEverything(t *testing.T) {
	lines := append([]string{rsOrigin}, rsLaunch("bg1")...)
	p, _ := stageStop(t, append(lines, "{torn")...)
	refusals, notice := stopOver(t, p)
	assert.Contains(t, joined(refusals), "feature-bg")
	assert.Empty(t, notice)
}

// Rules that refuse a range for the same reason are one line naming them, not one line each.
func TestGroupRefusals_OneLinePerDistinctReason(t *testing.T) {
	why := "not judged yet — run `sr-checks run --base a --head b` in /r"
	got := groupRefusals([]checkrun.FileGuardResult{
		{Attribution: "r1", Refused: true, Reason: why},
		{Attribution: "r2", Refused: true, Reason: why},
		{Attribution: "r3", Refused: true, Reason: "r3 says no"},
	}, []checkrun.CheckOutcome{{Rule: "r1", Subject: "x.md", Status: "missing"}, {Rule: "r2", Subject: "y.md", Status: "missing"}})
	require.Len(t, got, 2)
	assert.Equal(t, 1, strings.Count(strings.Join(got, "\n"), "not judged yet"))
	assert.Contains(t, got[0], "file-guard r1, r2")
	assert.Contains(t, got[0], "x.md")
	assert.Contains(t, got[0], "y.md")
	assert.Equal(t, "r3 says no (file-guard r3)", got[1])
}

// A row with no agent_id that holds the branch of a running agent's row is the same range.
func TestRunningAgentOf(t *testing.T) {
	running := map[string]bool{"bg1": true}
	branches := map[string]string{repoOf("/nowhere/r") + "\x00feature": "bg1"}
	tr := func(folder, head, agent string) sessionstate.TrackedRange {
		return sessionstate.TrackedRange{Folder: folder, Head: head, AgentID: agent, HeadSHA: "x"}
	}
	assert.Equal(t, "bg1", runningAgentOf(tr("/nowhere/r/wt", "feature", "bg1"), running, branches))
	assert.Equal(t, "bg1", runningAgentOf(tr("/nowhere/r", "feature", ""), running, branches))
	assert.Equal(t, "", runningAgentOf(tr("/nowhere/r", "main", ""), running, branches), "another branch is the root's own")
	assert.Equal(t, "", runningAgentOf(tr("/nowhere/r", "feature", "bg2"), running, branches), "a row of an agent that is not running")
}
