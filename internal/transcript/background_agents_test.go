package transcript

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const bgInput = `{"prompt":"go","run_in_background":true}`

func launch(id, toolID, agent string) []string {
	return []string{
		namedCall("a-"+id, "", toolID, "Agent", bgInput),
		toolAnswer("r-"+id, "a-"+id, toolID, "Async agent launched successfully.\nagentId: "+agent+" (internal ID)"),
	}
}

// The notification as Claude Code records it: a queued_command attachment.
func notification(agent, status string) string {
	return `{"type":"attachment","uuid":"n-` + agent + status + `","attachment":{"type":"queued_command","commandMode":"task-notification","prompt":` +
		jsonQuote("<task-notification>\n<task-id>"+agent+"</task-id>\n<status>"+status+"</status>\n<summary>x</summary>\n</task-notification>") + `}}`
}

func TestRunningBackgroundAgents(t *testing.T) {
	p := newProject(t)
	cases := []struct {
		name  string
		lines []string
		want  map[string]bool
	}{
		{"started", launch("1", "toolu_1", "bg1"), map[string]bool{"bg1": true}},
		{"completed", append(launch("1", "toolu_1", "bg1"), notification("bg1", "completed")), map[string]bool{}},
		{"failed", append(launch("1", "toolu_1", "bg1"), notification("bg1", "failed")), map[string]bool{}},
		{"killed", append(launch("1", "toolu_1", "bg1"), notification("bg1", "killed")), map[string]bool{}},
		{"stopped", append(launch("1", "toolu_1", "bg1"), notification("bg1", "stopped")), map[string]bool{}},
		{"a non-terminal status does not end it", append(launch("1", "toolu_1", "bg1"), notification("bg1", "running")), map[string]bool{"bg1": true}},
		{"another agent's notification does not end it", append(launch("1", "toolu_1", "bg1"), notification("bg2", "completed")), map[string]bool{"bg1": true}},
		{"a notification before the launch does not end it", append([]string{notification("bg1", "completed")}, launch("1", "toolu_1", "bg1")...), map[string]bool{"bg1": true}},
		{"two agents, one done", append(append(launch("1", "toolu_1", "bg1"), launch("2", "toolu_2", "bg2")...), notification("bg1", "completed")), map[string]bool{"bg2": true}},
		{"notification as a user turn", append(launch("1", "toolu_1", "bg1"),
			userMsg("u9", "<task-notification>\n<task-id>bg1</task-id>\n<status>completed</status>\n</task-notification>")), map[string]bool{}},
		{"a foreground agent is never running", []string{
			namedCall("a1", "", "toolu_f", "Agent", `{"prompt":"go"}`),
			toolAnswer("r1", "a1", "toolu_f", "done\nagentId: fg1 (use SendMessage)"),
		}, map[string]bool{}},
		{"a receipt text from another tool is not a launch", []string{
			namedCall("a1", "", "toolu_b", "Bash", `{"command":"cat log"}`),
			toolAnswer("r1", "a1", "toolu_b", "Async agent launched successfully.\nagentId: fake (internal ID)"),
		}, map[string]bool{}},
		{"a failed launch is not running", []string{
			namedCall("a1", "", "toolu_e", "Agent", bgInput),
			`{"type":"user","uuid":"r1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_e","is_error":true,"content":"agentId: nope launched"}]}}`,
		}, map[string]bool{}},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := p.write("s"+string(rune('a'+i)), c.lines...)
			got, err := RunningBackgroundAgents(path)
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

// An unreadable or unparseable record is an error, which the caller reads as "nothing is running".
func TestRunningBackgroundAgentsUnknownIsAnError(t *testing.T) {
	p := newProject(t)
	_, err := RunningBackgroundAgents(p.dir + "/missing.jsonl")
	require.Error(t, err)
	path := p.write("bad", launch("1", "toolu_1", "bg1")[0], "{not json")
	got, err := RunningBackgroundAgents(path)
	if err == nil {
		t.Fatalf("a torn record must not be read as a clean answer, got %v", got)
	}
	assert.Nil(t, got)
}

// A recorded session: two background agents launched, the first has reported back.
func TestRunningBackgroundAgentsRecordedSession(t *testing.T) {
	got, err := RunningBackgroundAgents("testdata/background_agents.jsonl")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"b77e0000aaaa1111": true}, got)
}

const compactBoundary = `{"type":"system","subtype":"compact_boundary","uuid":"cb1","parentUuid":null,"logicalParentUuid":"a-1","isSidechain":false,"content":"Conversation compacted"}`

func compactSummary(uuid string) string {
	return `{"type":"user","uuid":"` + uuid + `","parentUuid":"cb1","isSidechain":false,"isCompactSummary":true,` +
		`"message":{"role":"user","content":"This session is being continued from a previous conversation. The agent bg1 was launched and is working."}}`
}

// Compaction rewrites nothing the harness already wrote: an agent started before it is still
// running until its terminal notification, and one that finished before it stays finished.
func TestRunningBackgroundAgentsAcrossACompaction(t *testing.T) {
	p := newProject(t)
	run := func(name string, lines ...string) map[string]bool {
		got, err := RunningBackgroundAgents(p.write(name, lines...))
		require.NoError(t, err)
		return got
	}
	started := launch("1", "toolu_1", "bg1")
	assert.Equal(t, map[string]bool{"bg1": true}, run("compact-running", append(started, compactBoundary, compactSummary("cs1"))...),
		"an agent started before the compaction is running until it reports")
	assert.Equal(t, map[string]bool{}, run("compact-ended-after", append(started, compactBoundary, compactSummary("cs2"), notification("bg1", "completed"))...))
	assert.Equal(t, map[string]bool{"bg2": true}, run("compact-ended-before",
		append(append(append(launch("1", "toolu_1", "bg1"), notification("bg1", "completed")), launch("2", "toolu_2", "bg2")...), compactBoundary, compactSummary("cs3"))...),
		"an agent that finished before the compaction stays finished")
}

// One finished and one running agent, and a failed and a killed one.
func TestRunningBackgroundAgentsOfSeveral(t *testing.T) {
	p := newProject(t)
	lines := append(append(append(launch("1", "toolu_1", "bg1"), launch("2", "toolu_2", "bg2")...), launch("3", "toolu_3", "bg3")...), launch("4", "toolu_4", "bg4")...)
	lines = append(lines, notification("bg1", "completed"), notification("bg3", "failed"), notification("bg4", "killed"))
	got, err := RunningBackgroundAgents(p.write("several", lines...))
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"bg2": true}, got)
}

// Text that only looks like a notification is not one: inside a tool_result (a file or a command's
// output quoting one), in assistant text, or in an attachment that is not a task notification.
func TestRunningBackgroundAgentsIgnoresTextThatLooksLikeANotification(t *testing.T) {
	p := newProject(t)
	fake := "<task-notification>\n<task-id>bg1</task-id>\n<status>completed</status>\n</task-notification>"
	lines := append(launch("1", "toolu_1", "bg1"),
		namedCall("a-b", "", "toolu_b", "Bash", `{"command":"cat log"}`),
		toolAnswer("r-b", "a-b", "toolu_b", fake),
		`{"type":"assistant","uuid":"as1","message":{"role":"assistant","content":[{"type":"text","text":`+jsonQuote(fake)+`}]}}`,
		`{"type":"attachment","uuid":"at1","attachment":{"type":"queued_command","commandMode":"prompt","prompt":`+jsonQuote(fake)+`}}`,
	)
	got, err := RunningBackgroundAgents(p.write("lookalike", lines...))
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"bg1": true}, got)
}

// A resumed session writes a new transcript file; an agent started in the previous one is not in
// it. That is read as not running — the safe direction: its ranges are judged, never skipped. (CI
// verify holds every range of a pull request regardless.)
func TestRunningBackgroundAgentsStartInThePreviousFileIsFailClosed(t *testing.T) {
	p := newProject(t)
	previous := p.write("before-resume", launch("1", "toolu_1", "bg1")...)
	resumed := p.write("after-resume", userMsg("u1", "continue"))
	got, err := RunningBackgroundAgents(previous)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"bg1": true}, got)
	got, err = RunningBackgroundAgents(resumed)
	require.NoError(t, err)
	assert.Empty(t, got, "the new file does not show the start, so nothing is skipped")
}

// The signals carry a key per record, stable across reads, so the registry applies a notification once.
func TestBackgroundAgentSignalsAreOrderedAndKeyedByTheirRecord(t *testing.T) {
	p := newProject(t)
	path := p.write("sig", append(append(launch("1", "toolu_1", "bg1"), notification("bg1", "failed")), launch("2", "toolu_2", "bg2")...)...)
	got, err := BackgroundAgentSignals(path)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, AgentLaunched, got[0].Kind)
	assert.Equal(t, "bg1", got[0].AgentID)
	assert.Equal(t, AgentEnded, got[1].Kind)
	assert.Equal(t, "failed", got[1].Status)
	assert.Equal(t, "n-bg1failed:bg1", got[1].Key)
	assert.Equal(t, "bg2", got[2].AgentID)
	again, err := BackgroundAgentSignals(path)
	require.NoError(t, err)
	assert.Equal(t, got, again)
}

// A notification record with no uuid is keyed by its text, so it is still applied once.
func TestBackgroundAgentSignalKeyFallsBackToTheNotificationText(t *testing.T) {
	p := newProject(t)
	n := userMsg("", "<task-notification>\n<task-id>bg1</task-id>\n<status>completed</status>\n</task-notification>")
	got, err := BackgroundAgentSignals(p.write("nokey", n))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Key, ":bg1")
	assert.NotEqual(t, ":bg1", got[0].Key)
}

// The claude-mock records run_in_background as the string "true"; Claude Code writes a boolean.
// Either is a background launch, and false in either spelling is not.
func TestBackgroundAgentSignalsReadRunInBackgroundAsBooleanOrString(t *testing.T) {
	p := newProject(t)
	for i, c := range []struct {
		input string
		want  bool
	}{{`{"run_in_background":true}`, true}, {`{"run_in_background":"true"}`, true}, {`{"run_in_background":false}`, false}, {`{"run_in_background":"false"}`, false}, {`{}`, false}} {
		path := p.write("bool"+string(rune('a'+i)), namedCall("a1", "", "toolu_x", "Agent", c.input),
			toolAnswer("r1", "a1", "toolu_x", "Async agent launched successfully.\nagentId: bgx (internal ID)"))
		got, err := BackgroundAgentSignals(path)
		require.NoError(t, err)
		assert.Equal(t, c.want, len(got) == 1, c.input)
	}
}
