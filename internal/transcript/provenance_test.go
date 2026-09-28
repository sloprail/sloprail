package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	agentRecord := p.write("agent-bg1", harnessRecordLines("TRANSCRIPTMARKER all 40 tests pass")...)
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

// harnessRecordLines is a record as the harness writes one: the fields only it
// writes (sessionId, parentUuid, version, userType) beside the conversation.
func harnessRecordLines(text string) []string {
	return []string{
		`{"parentUuid":null,"isSidechain":true,"userType":"external","cwd":"/w","sessionId":"sess-1","version":"2.1.282","type":"user","uuid":"s0","message":{"role":"user","content":"go"}}`,
		`{"parentUuid":"s0","isSidechain":true,"userType":"external","cwd":"/w","sessionId":"sess-1","version":"2.1.282","type":"assistant","uuid":"s1","message":{"role":"assistant","content":[{"type":"text","text":` + jsonQuote(text) + `}]}}`,
	}
}

// A transcript read back is recognised by its text too: a cat of a record the
// command then deletes, or one named relative to a directory the record does
// not give, still returns model-written text. An ordinary JSON-lines data file
// — even one whose lines carry a uuid and a type — and a command that merely
// mentions the projects directory in a comment are ordinary tool output.
func TestTranscriptReadBackIsRecognisedByItsText(t *testing.T) {
	p := newProject(t)
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	gone := filepath.Join(t.TempDir(), "agent.jsonl")
	lines := harnessRecordLines("GONEMARKER all green")
	data := `{"uuid":"d1","type":"user","message":{"n":1},"value":"DATAMARKER 42"}`
	path := p.write("the-session",
		userMsg("u1", "check it"),
		namedCall("a1", "u1", "toolu_cat", "Bash", `{"command":"cat `+gone+` && rm `+gone+`"}`),
		toolAnswer("r1", "a1", "toolu_cat", lines[0]+"\n"+lines[1]),
		namedCall("a2", "r1", "toolu_rel", "Bash", `{"command":"cat agent.jsonl"}`),
		toolAnswer("r2", "a2", "toolu_rel", "RELMARKER "+lines[1]),
		namedCall("a3", "r2", "toolu_data", "Bash", `{"command":"cat data.jsonl"}`),
		toolAnswer("r3", "a3", "toolu_data", data),
		namedCall("a4", "r3", "toolu_echo", "Bash", `{"command":"echo BUILD-OK-5512 # not reading `+cfg+`/projects"}`),
		toolAnswer("r4", "a4", "toolu_echo", "BUILD-OK-5512"),
	)
	for _, q := range []string{"GONEMARKER", "RELMARKER"} {
		_, err := ResolveCitation(path, toolReq(q))
		require.Error(t, err, "%s: an agent transcript read back grounded as tool output", q)
		assert.Contains(t, err.Error(), "agent transcript", q)
	}
	for _, q := range []string{"DATAMARKER 42", "BUILD-OK-5512"} {
		_, err := ResolveCitation(path, toolReq(q))
		assert.NoError(t, err, "%s is ordinary tool output", q)
	}
}

// Classifying a long record's calls stays cheap: a record of 3000 shell calls,
// each naming a file, resolves a citation well within a hook's budget.
func TestLongRecordResolvesQuickly(t *testing.T) {
	p := newProject(t)
	dir := t.TempDir()
	lines := []string{userMsg("u0", "go")}
	for i := 0; i < 3000; i++ {
		id := fmt.Sprintf("t%d", i)
		f := filepath.Join(dir, fmt.Sprintf("f%d.txt", i))
		lines = append(lines,
			namedCall(fmt.Sprintf("a%d", i), "u0", id, "Bash", `{"command":"cat `+f+` | grep -n x -- `+f+`"}`),
			toolAnswer(fmt.Sprintf("r%d", i), fmt.Sprintf("a%d", i), id, fmt.Sprintf("output %d", i)))
	}
	lines = append(lines, namedCall("aZ", "u0", "tZ", "Bash", `{"command":"make test"}`), toolAnswer("rZ", "aZ", "tZ", "LONGMARKER passed"))
	path := p.write("the-session", lines...)
	start := time.Now()
	_, err := ResolveCitation(path, toolReq("LONGMARKER"))
	require.NoError(t, err)
	took := time.Since(start)
	t.Logf("resolved over 3000 calls in %s", took)
	assert.Less(t, took, 1500*time.Millisecond, "a citation over a long record must stay cheap")
}
