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
