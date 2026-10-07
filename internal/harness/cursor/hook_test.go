package cursor

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures under testdata are excerpts of REAL recorded cursor-agent runs
// (cursor-agent 2026.09.28) from the harness-mocks repository at commit ef10d81e:
//
//	file-tools.payloads.jsonl      cursor-mock/snapshots/runs/file-tools/samples/20261001-125922/payloads.jsonl
//	file-tools.transcript.jsonl    .../file-tools/samples/20261001-125922/transcript/<id>/<id>.jsonl
//	pretool-refusal.payloads.jsonl cursor-mock/snapshots/runs/pretool-refusal/samples/20261001-124607/payloads.jsonl
//	subagent.payloads.jsonl        cursor-mock/snapshots/runs/subagent-lifecycle-hooks/samples/20261003-151918/payloads.jsonl
//
// Only hook payload lines are kept (the run's own bookkeeping lines and
// afterAgentThought are dropped). <RUN>, <TMP> are the recordings' own scrub
// placeholders.

func fixture(t *testing.T, name string) []Payload {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	require.NoError(t, err)
	defer f.Close()
	var out []Payload
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		out = append(out, ReadHook(strings.NewReader(sc.Text())))
	}
	require.NoError(t, sc.Err())
	return out
}

func find(t *testing.T, ps []Payload, e Event, pick func(Payload) bool) Payload {
	t.Helper()
	for _, p := range ps {
		if p.HookEventName == e && (pick == nil || pick(p)) {
			return p
		}
	}
	t.Fatalf("no %s payload", e)
	return Payload{}
}

func TestCommonFieldsOfARecordedSessionStart(t *testing.T) {
	p := find(t, fixture(t, "file-tools.payloads.jsonl"), SessionStart, nil)
	assert.Equal(t, "c1cba5e7-b85d-487e-8b02-95381492cadb", p.SessionID)
	assert.Equal(t, p.SessionID, p.ConversationID)
	assert.Equal(t, []string{"<RUN>"}, p.WorkspaceRoots)
	assert.Equal(t, "<RUN>", p.Folder(), "the folder is the workspace root: no cwd is carried")
	assert.Empty(t, p.Transcript(), "transcript_path is null at sessionStart")
	assert.Equal(t, "start", p.HookEventName.HookPoint())
}

func TestRecordedWriteExposesPathAndContentAtPreToolUse(t *testing.T) {
	ps := fixture(t, "file-tools.payloads.jsonl")
	p := find(t, ps, PreToolUse, func(p Payload) bool { return p.ToolName == "Write" })
	path, content, ok := p.FileWrite()
	require.True(t, ok)
	assert.Equal(t, "<RUN>/note.txt", path)
	assert.Equal(t, "hi\n", content)
	assert.Contains(t, p.Transcript(), "agent-transcripts/c1cba5e7")
	assert.Equal(t, "pre-tool", p.HookEventName.HookPoint())
}

func TestRecordedEditIsAWriteWithTheNewContent(t *testing.T) {
	ps := fixture(t, "file-tools.payloads.jsonl")
	var last Payload
	for _, p := range ps {
		if p.HookEventName == PreToolUse && p.ToolName == "Write" {
			last = p
		}
	}
	_, content, ok := last.FileWrite()
	require.True(t, ok)
	assert.Equal(t, "bye\n", content)

	edit := find(t, ps, AfterFileEdit, func(p Payload) bool { return p.Edits[0].OldString == "hi" })
	assert.Equal(t, "<RUN>/note.txt", edit.FilePath)
	assert.Equal(t, "bye", edit.Edits[0].NewString)
}

func TestRecordedShellEventsCarryTheCommand(t *testing.T) {
	ps := fixture(t, "pretool-refusal.payloads.jsonl")
	pre := find(t, ps, PreToolUse, nil)
	cmd, ok := pre.ShellCommand()
	require.True(t, ok)
	assert.Equal(t, "echo DENYME", cmd)
	assert.Equal(t, "<RUN>", pre.Folder(), "a Shell event's cwd is recorded empty; the root names the folder")

	before := find(t, ps, BeforeShellExecution, nil)
	cmd, ok = before.ShellCommand()
	require.True(t, ok)
	assert.Equal(t, "echo EXIT2SHELL", cmd)
	assert.Empty(t, before.HookEventName.HookPoint(), "preToolUse already covers the shell call")
	_, _, isWrite := pre.FileWrite()
	assert.False(t, isWrite)
}

func TestRecordedSubagentCallIsATaskToolUse(t *testing.T) {
	ps := fixture(t, "subagent.payloads.jsonl")
	task := find(t, ps, PreToolUse, func(p Payload) bool { return p.ToolName == "Task" })
	sub := find(t, ps, PreToolUse, func(p Payload) bool { return p.ToolName == "Shell" })
	assert.NotEqual(t, task.SessionID, sub.SessionID, "a sub-agent's events carry its own session id")
}

func TestFolderFallsBackToTheEnvironmentThenAPlainCwd(t *testing.T) {
	t.Setenv("CURSOR_PROJECT_DIR", "")
	assert.Equal(t, "/p", Payload{Cwd: "file:///p"}.Folder(), "a file:// cwd is read as a path")
	assert.Equal(t, "", Payload{Cwd: "relative"}.Folder())
	t.Setenv("CURSOR_PROJECT_DIR", "/env")
	assert.Equal(t, "/env", Payload{Cwd: "/p"}.Folder())
}

func TestSkillLoadIsAReadOfSKILLmd(t *testing.T) {
	read := func(path string) Payload {
		in, _ := json.Marshal(map[string]string{"file_path": path})
		return Payload{HookEventName: PreToolUse, ToolName: "Read", ToolInput: in}
	}
	name, ok := read("/r/.agents/skills/authoring/SKILL.md").SkillLoaded()
	assert.True(t, ok)
	assert.Equal(t, "authoring", name)
	name, ok = read("/r/.cursor/skills/x/SKILL.md").SkillLoaded()
	assert.True(t, ok)
	assert.Equal(t, "x", name)
	_, ok = read("/r/docs/SKILL.md").SkillLoaded()
	assert.False(t, ok, "a SKILL.md outside a skills directory is not a skill")
	_, ok = read("/r/.agents/skills/authoring/notes.md").SkillLoaded()
	assert.False(t, ok)
}

func TestOutputShapes(t *testing.T) {
	var b bytes.Buffer
	require.NoError(t, DenyOutput(&b, "no"))
	var got map[string]any
	require.NoError(t, json.Unmarshal(b.Bytes(), &got))
	assert.Equal(t, "deny", got["permission"])
	assert.Equal(t, "no", got["user_message"], "the recorded run shows only user_message reaching the agent")
	assert.Equal(t, "no", got["agent_message"])

	b.Reset()
	require.NoError(t, BlockOutput(&b, "keep going"))
	assert.JSONEq(t, `{"followup_message":"keep going"}`, b.String())

	b.Reset()
	require.NoError(t, ContextOutput(&b, "ctx"))
	assert.JSONEq(t, `{"additional_context":"ctx"}`, b.String())
}

func TestDetection(t *testing.T) {
	ps, err := os.ReadFile("testdata/pretool-refusal.payloads.jsonl")
	require.NoError(t, err)
	first := bytes.SplitN(ps, []byte("\n"), 2)[0]
	assert.True(t, IsCursorPayload(first))
	assert.False(t, IsCursorPayload([]byte(`{"hook_event_name":"PreToolUse","session_id":"x"}`)), "a Claude payload has no cursor_version")
	assert.False(t, IsCursorPayload([]byte(`not json`)))
}

func TestEmptyBodyIsAZeroPayload(t *testing.T) {
	assert.Equal(t, Payload{}.HookEventName, ReadHook(strings.NewReader("")).HookEventName)
	assert.Equal(t, "", ReadHook(strings.NewReader("{")).SessionID)
}

func TestEventHookPoints(t *testing.T) {
	assert.Equal(t, "stop", Stop.HookPoint())
	assert.Equal(t, "subagent-start", SubagentStart.HookPoint())
	assert.Equal(t, "subagent-stop", SubagentStop.HookPoint())
	assert.Equal(t, "", PostToolUse.HookPoint())
}
