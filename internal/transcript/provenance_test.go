package transcript

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// namedCall is an assistant tool_use with an arbitrary JSON input.
func namedCall(uuid, parent, id, name, input string) string {
	return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":"` + parent + `","isSidechain":false,` +
		`"message":{"role":"assistant","content":[{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}]}}`
}

// A result whose call is not in the record is of unknown provenance — it could
// answer a sub-agent dispatch in a file split off by a restart — so it is not
// tool output.
func TestOrphanToolResultIsNotCitable(t *testing.T) {
	p := newProject(t)
	path := p.write("the-session",
		userMsg("u1", "run it"),
		toolAnswer("r1", "u1", "toolu_elsewhere", "ORPHANMARKER all green"),
		namedCall("a1", "r1", "toolu_here", "Bash", `{"command":"make"}`),
		toolAnswer("r2", "a1", "toolu_here", "KNOWNMARKER all green"),
	)
	_, err := ResolveCitation(path, toolReq("ORPHANMARKER"))
	assert.Error(t, err, "a result of unknown provenance grounded as tool output")
	_, isToolResult, err := ToolResultAt(path, 2)
	require.NoError(t, err)
	assert.False(t, isToolResult)

	got, err := ResolveCitation(path, toolReq("KNOWNMARKER"))
	require.NoError(t, err, "a result answering a call in the record is tool output")
	assert.Equal(t, 4, got.Line)
}

// TaskOutput returns a background task's output: a background Bash's is the
// command's, a background agent's is the agent's reply. The task is classified
// by the receipt its launch got back — the shapes Claude Code writes — and an
// unclassifiable task is not citable.
func TestTaskOutputIsCitableOnlyForABackgroundBash(t *testing.T) {
	p := newProject(t)
	path := p.write("the-session",
		userMsg("u1", "run it in the background"),
		namedCall("a1", "u1", "toolu_bg", "Bash", `{"command":"make test","run_in_background":true}`),
		toolAnswer("r1", "a1", "toolu_bg", "Command running in background with ID: b04wesqb9. Output is being written to: /tmp/tasks/b04wesqb9.output. You will be notified when it completes."),
		namedCall("a2", "r1", "toolu_ag", "Agent", `{"prompt":"go","run_in_background":true}`),
		toolAnswer("r2", "a2", "toolu_ag", "Async agent launched successfully. (This tool result is internal metadata.)\nagentId: a212039c305225560 (internal ID - do not mention to user.)"),
		namedCall("a3", "r2", "toolu_out_bash", "TaskOutput", `{"task_id":"b04wesqb9","block":true}`),
		toolAnswer("r3", "a3", "toolu_out_bash", "BASHTASKMARKER 12 passed"),
		namedCall("a4", "r3", "toolu_out_agent", "TaskOutput", `{"task_id":"a212039c305225560","block":true}`),
		toolAnswer("r4", "a4", "toolu_out_agent", "AGENTTASKMARKER all 40 tests pass"),
		namedCall("a5", "r4", "toolu_out_unknown", "TaskOutput", `{"task_id":"zzz999","block":true}`),
		toolAnswer("r5", "a5", "toolu_out_unknown", "UNKNOWNTASKMARKER done"),
	)

	got, err := ResolveCitation(path, toolReq("BASHTASKMARKER"))
	require.NoError(t, err, "a background Bash's output read through TaskOutput is tool output")
	assert.Equal(t, 7, got.Line)
	assert.Contains(t, got.Call, "TaskOutput")

	_, err = ResolveCitation(path, toolReq("AGENTTASKMARKER"))
	assert.Error(t, err, "a background agent's reply read through TaskOutput grounded as tool output")
	_, err = ResolveCitation(path, toolReq("UNKNOWNTASKMARKER"))
	assert.Error(t, err, "a task that cannot be classified must fail closed")
	_, err = ResolveCitation(path, toolReq("agentId: a212039c305225560"))
	assert.Error(t, err, "an agent's launch receipt is not tool output")
}
