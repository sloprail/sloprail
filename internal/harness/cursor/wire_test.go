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

	"github.com/sloprail/sloprail/internal/harness"
)

// The seam's view of recorded payloads (testdata/*.payloads.jsonl, see hook_test.go;
// delete.payloads.jsonl is a real cursor-agent 2026.10.01 run, 2026-10-07, with a
// Delete the harness-mocks recordings do not have).

func hookInputs(t *testing.T, name string) []harness.HookInput {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	require.NoError(t, err)
	defer f.Close()
	var out []harness.HookInput
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		out = append(out, New().ParseHook(strings.NewReader(sc.Text())))
	}
	require.NoError(t, sc.Err())
	return out
}

func inputOf(t *testing.T, ins []harness.HookInput, event string, pick func(harness.HookInput) bool) harness.HookInput {
	t.Helper()
	for _, in := range ins {
		if in.Event == event && (pick == nil || pick(in)) {
			return in
		}
	}
	t.Fatalf("no %s input", event)
	return harness.HookInput{}
}

func TestParseHookNamesTheFolderAndTheConversationAndNoTranscriptYet(t *testing.T) {
	ins := hookInputs(t, "file-tools.payloads.jsonl")
	start := inputOf(t, ins, "sessionStart", nil)
	assert.Equal(t, "<RUN>", start.Cwd, "Cursor reports no cwd: the folder is the workspace root")
	assert.Equal(t, "c1cba5e7-b85d-487e-8b02-95381492cadb", start.SessionID)
	assert.Empty(t, start.TranscriptPath, "a null transcript_path is no path, for the locator to fill in")
}

func TestParseHookWriteIsTheCanonicalWrite(t *testing.T) {
	in := inputOf(t, hookInputs(t, "file-tools.payloads.jsonl"), "preToolUse", func(in harness.HookInput) bool { return in.ToolName == harness.ToolWrite })
	var args struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
	}
	require.NoError(t, json.Unmarshal(in.ToolInput, &args))
	assert.Equal(t, "<RUN>/note.txt", args.FilePath)
	assert.Equal(t, "hi\n", args.Content)
	assert.Empty(t, in.Files)
}

func TestParseHookShellIsBash(t *testing.T) {
	var in harness.HookInput
	for _, c := range hookInputs(t, "pretool-refusal.payloads.jsonl") {
		if c.Event == "preToolUse" && c.ToolName == harness.ToolBash {
			in = c
		}
	}
	require.Equal(t, harness.ToolBash, in.ToolName, "Shell is the canonical Bash")
	var args struct {
		Command string `json:"command"`
	}
	require.NoError(t, json.Unmarshal(in.ToolInput, &args))
	assert.NotEmpty(t, args.Command)
}

func TestParseHookDeleteIsAFileEffect(t *testing.T) {
	in := inputOf(t, hookInputs(t, "delete.payloads.jsonl"), "preToolUse", nil)
	assert.Equal(t, "Delete", in.ToolName)
	require.Len(t, in.Files, 1)
	assert.Equal(t, harness.FileDelete, in.Files[0].Kind)
	assert.Equal(t, "<RUN>/gone.txt", in.Files[0].Path)
	assert.NotEmpty(t, in.TranscriptPath, "a later event names the transcript")
}

func TestParseHookStopCarriesTheLoopAsStopHookActive(t *testing.T) {
	first := New().ParseHook(strings.NewReader(`{"hook_event_name":"stop","conversation_id":"c","status":"completed","loop_count":0}`))
	again := New().ParseHook(strings.NewReader(`{"hook_event_name":"stop","conversation_id":"c","status":"completed","loop_count":1}`))
	assert.False(t, first.StopHookActive)
	assert.True(t, again.StopHookActive, "a follow-up re-prompt already happened")
	assert.Equal(t, "c", first.SessionID, "falls back to conversation_id")
}

func TestParseHookEmptyBodyIsAZeroInput(t *testing.T) {
	assert.Equal(t, harness.HookInput{}, New().ParseHook(strings.NewReader("")))
}

func TestLocateTranscriptDerivesTheFileCursorWillWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	start := New().ParseHook(strings.NewReader(`{"hook_event_name":"sessionStart","conversation_id":"abc","session_id":"abc","workspace_roots":["/work/proj"],"transcript_path":null}`))
	got := New().(harness.TranscriptLocator).LocateTranscript(start)
	assert.Equal(t, home+"/.cursor/projects/work-proj/agent-transcripts/abc/abc.jsonl", got)
}

func render(t *testing.T, resp harness.HookResponse) string {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, New().RenderHook(&b, resp))
	return b.String()
}

func TestRenderHookShapes(t *testing.T) {
	deny := render(t, harness.HookResponse{Event: "preToolUse", Decision: harness.Deny, Reason: "no"})
	var d map[string]any
	require.NoError(t, json.Unmarshal([]byte(deny), &d))
	assert.Equal(t, map[string]any{"permission": "deny", "user_message": "no", "agent_message": "no"}, d)

	block := render(t, harness.HookResponse{Event: "stop", Decision: harness.Block, Reason: "keep going"})
	assert.JSONEq(t, `{"followup_message":"keep going"}`, block)

	ctx := render(t, harness.HookResponse{Event: "sessionStart", AdditionalContext: "hello"})
	assert.JSONEq(t, `{"additional_context":"hello"}`, ctx)

	assert.Empty(t, render(t, harness.HookResponse{Event: "preToolUse"}), "an allow with nothing to say writes nothing")
	assert.Empty(t, render(t, harness.HookResponse{Event: "preToolUse", SystemMessage: "note"}), "Cursor has no person-only message on an allow")
}

func TestJudgeRefusalUsesTheGrantInTheHookEnvironment(t *testing.T) {
	g := JudgeGrant{Writable: []string{"/out"}, Readonly: []string{"/proj"}}
	env := func(k string) string {
		if k == JudgeGrantEnv {
			b, _ := json.Marshal(g)
			return string(b)
		}
		return ""
	}
	gate := New().(harness.JudgeGate)
	in := func(tool, path string) harness.HookInput {
		return harness.HookInput{Event: "preToolUse", ToolName: tool, ToolInput: json.RawMessage(`{"file_path":"` + path + `"}`)}
	}
	assert.Empty(t, gate.JudgeRefusal(in("Write", "/out/answer.txt"), env))
	assert.Contains(t, gate.JudgeRefusal(in("Write", "/proj/a.go"), env), "may read /proj")
	assert.Contains(t, gate.JudgeRefusal(in("Delete", "/elsewhere/x"), env), "only its answer file")
	assert.Empty(t, gate.JudgeRefusal(in("Read", "/proj/a.go"), env), "reads are not gated")
	assert.Empty(t, gate.JudgeRefusal(in("Write", "/proj/a.go"), func(string) string { return "" }), "not a launched judge")
	assert.Contains(t, gate.JudgeRefusal(in("Write", "/out/answer.txt"), func(string) string { return "{not json" }), "only its answer file", "an unreadable grant allows nothing")
}

func TestDetectAndChildEnv(t *testing.T) {
	h := New().(harness.Detector)
	assert.True(t, h.Detect([]string{"PATH=/bin", "CURSOR_AGENT=1"}))
	assert.True(t, h.Detect([]string{PluginRootEnv + "=/p"}))
	assert.False(t, h.Detect([]string{"CURSOR_VERSION=1", "CURSOR_PROJECT_DIR=/p"}), "an editor terminal is not cursor-agent")
	assert.ElementsMatch(t, []string{"CURSOR_CONVERSATION_ID", "CURSOR_REQUEST_ID", "CURSOR_TRANSCRIPT_PATH"}, New().(harness.ChildEnvBlocklist).ChildEnvBlocklist())
}

func TestCursorSelectedByNameAndRegisteredByImport(t *testing.T) {
	assert.Equal(t, "cursor", harness.Select([]string{harness.SelectEnv + "=cursor"}).Name())
	assert.Equal(t, "cursor", harness.Select([]string{"CURSOR_AGENT=1"}).Name())
}
