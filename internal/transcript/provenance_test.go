package transcript

import (
	"os"
	"path/filepath"
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

// TaskOutput returned a background task's output — an agent's reply as
// readily as a command's — and no current harness calls it: its results are
// never citable, whatever launched the task, so a legacy record cannot launder
// an agent's reply through it.
func TestTaskOutputIsNeverCitable(t *testing.T) {
	p := newProject(t)
	path := p.write("the-session",
		userMsg("u1", "run it in the background"),
		namedCall("a1", "u1", "toolu_bg", "Bash", `{"command":"make test","run_in_background":true}`),
		toolAnswer("r1", "a1", "toolu_bg", "Command running in background with ID: b04wesqb9. Output is being written to: /tmp/tasks/b04wesqb9.output."),
		namedCall("a3", "r1", "toolu_out_bash", "TaskOutput", `{"task_id":"b04wesqb9","block":true}`),
		toolAnswer("r3", "a3", "toolu_out_bash", "BASHTASKMARKER 12 passed"),
	)
	_, err := ResolveCitation(path, toolReq("BASHTASKMARKER"))
	require.Error(t, err, "TaskOutput's result grounded as tool output")
	assert.Contains(t, err.Error(), "TaskOutput returned")
}

// A tool that reads an agent's transcript returns model-written text. A Read of
// a sub-agent's record, a `cat` of a background agent's `tasks/<id>.output` (a
// symbolic link to that agent's record), a copy of a record outside the
// projects directory — none grounds as tool output, and the refusal says why.
// An ordinary file read the same ways still does.
func TestReadingAnAgentTranscriptIsNotToolOutput(t *testing.T) {
	p := newProject(t)
	agentRecord := p.write("agent-bg1",
		`{"type":"user","uuid":"s0","parentUuid":null,"isSidechain":true,"message":{"role":"user","content":"go"}}`,
		`{"type":"assistant","uuid":"s1","parentUuid":"s0","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"TRANSCRIPTMARKER all 40 tests pass"}]}}`,
	)
	tasks := filepath.Join(t.TempDir(), "tasks")
	require.NoError(t, os.MkdirAll(tasks, 0o755))
	link := filepath.Join(tasks, "bg1.output")
	require.NoError(t, os.Symlink(agentRecord, link))
	plain := filepath.Join(t.TempDir(), "notes.txt")
	require.NoError(t, os.WriteFile(plain, []byte("PLAINMARKER ok\n"), 0o644))

	body := "TRANSCRIPTMARKER all 40 tests pass"
	path := p.write("the-session",
		userMsg("u1", "check it"),
		namedCall("a1", "u1", "toolu_read", "Read", `{"file_path":"`+agentRecord+`"}`),
		toolAnswer("r1", "a1", "toolu_read", "READ1 "+body),
		namedCall("a2", "r1", "toolu_cat", "Bash", `{"command":"cat `+link+`"}`),
		toolAnswer("r2", "a2", "toolu_cat", "CAT2 "+body),
		namedCall("a3", "r2", "toolu_plain", "Read", `{"file_path":"`+plain+`"}`),
		toolAnswer("r3", "a3", "toolu_plain", "PLAINMARKER ok"),
	)
	for _, q := range []string{"READ1", "CAT2"} {
		_, err := ResolveCitation(path, toolReq(q))
		require.Error(t, err, "%s: an agent transcript read back grounded as tool output", q)
		assert.Contains(t, err.Error(), "agent transcript", q)
	}
	got, err := ResolveCitation(path, toolReq("PLAINMARKER"))
	require.NoError(t, err, "an ordinary file read is tool output")
	assert.Equal(t, 7, got.Line)
}
