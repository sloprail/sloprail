package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/harness"
	_ "github.com/sloprail/sloprail/internal/harness/cursor"
	cursorrecord "github.com/sloprail/sloprail/internal/harness/cursor/record"
)

// On Cursor the transcript holds no tool output; sloprail keeps it from the post-tool
// hook and the engine's readers see it as tool_result records, so a quote of a tool's
// output grounds, and the tool call behind it is found by the id the merge gave it.
func TestCursorToolOutputGroundsACitation(t *testing.T) {
	t.Setenv(harness.SelectEnv, "cursor")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	const id = "8e69a2cf-c829-44fd-a77a-2c4c7c7611c0"

	dir := filepath.Join(t.TempDir(), "agent-transcripts", id)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, id+".jsonl")
	body := `{"role":"user","message":{"content":[{"type":"text","text":"<user_query>\nrun it\n</user_query>"}]}}` + "\n" +
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":{"command":"echo MARKER-7f3a"}}]}}` + "\n" +
		`{"role":"assistant","message":{"content":[{"type":"text","text":"done"}]}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))

	// Nothing recorded yet: the output is not in the record.
	got, err := CiteWithSources(path, "MARKER-7f3a", []SourceType{SourceToolResult})
	require.NoError(t, err)
	assert.Empty(t, got)

	// The post-tool hook runs.
	rec := harness.Current().(harness.ToolResultRecorder)
	input := json.RawMessage(`{"command":"echo MARKER-7f3a"}`)
	require.NoError(t, rec.RecordToolResult(harness.HookInput{
		SessionID: id, Event: "preToolUse", ToolUseID: "t1", ToolName: "Bash", ToolInput: input,
	}))
	require.NoError(t, rec.RecordToolResult(harness.HookInput{
		SessionID: id, Event: "postToolUse", ToolUseID: "t1", ToolName: "Bash", ToolInput: input,
		Result: &harness.ToolResult{Output: "MARKER-7f3a\n"},
	}))

	got, err = CiteWithSources(path, "MARKER-7f3a", []SourceType{SourceToolResult})
	require.NoError(t, err)
	require.Len(t, got, 1, "the recorded output grounds the quote")
	assert.Equal(t, cursorrecord.ResultLineBase+2, got[0].Line, "a stored result is numbered by its line of the store (the post, second), apart from the transcript's lines")

	text, isResult, err := ToolResultAt(path, got[0].Line)
	require.NoError(t, err)
	assert.True(t, isResult)
	assert.Equal(t, "MARKER-7f3a\n", text)

	entries, err := Read(path)
	require.NoError(t, err)
	require.Len(t, entries, 4)
	calls := ToolCalls(entries[1])
	require.Len(t, calls, 1)
	assert.Equal(t, "Bash", calls[0].Name, "the call is the canonical Bash")
	assert.Equal(t, "cursor-L2-0", calls[0].ID, "joined to its result by id")
}

// A result appended later never renumbers what was cited: transcript lines keep their
// physical numbers and an earlier result keeps its own.
func TestCursorCitedLinesDoNotShiftWhenMoreIsAppended(t *testing.T) {
	t.Setenv(harness.SelectEnv, "cursor")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	const id = "9f69a2cf-c829-44fd-a77a-2c4c7c7611c1"
	dir := filepath.Join(t.TempDir(), "agent-transcripts", id)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, id+".jsonl")
	call := func(c string) string {
		return `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":{"command":"` + c + `"}}]}}` + "\n"
	}
	body := call("echo AAA-1") + call("echo BBB-2")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	rec := harness.Current().(harness.ToolResultRecorder)
	feed := func(tid, c, out string) {
		in := json.RawMessage(`{"command":"` + c + `"}`)
		require.NoError(t, rec.RecordToolResult(harness.HookInput{SessionID: id, Event: "preToolUse", ToolUseID: tid, ToolName: "Bash", ToolInput: in}))
		require.NoError(t, rec.RecordToolResult(harness.HookInput{SessionID: id, Event: "postToolUse", ToolUseID: tid, ToolName: "Bash", ToolInput: in, Result: &harness.ToolResult{Output: out}}))
	}
	feed("a", "echo AAA-1", "AAA-1\n")
	before, err := CiteWithSources(path, "AAA-1", []SourceType{SourceToolResult})
	require.NoError(t, err)
	require.Len(t, before, 1)

	feed("b", "echo BBB-2", "BBB-2\n")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(call("echo CCC-3"))
	require.NoError(t, err)
	require.NoError(t, f.Close())
	feed("c", "echo CCC-3", "CCC-3\n")

	after, err := CiteWithSources(path, "AAA-1", []SourceType{SourceToolResult})
	require.NoError(t, err)
	assert.Equal(t, before, after, "the earlier citation is unchanged")
	later, err := CiteWithSources(path, "BBB-2", []SourceType{SourceToolResult})
	require.NoError(t, err)
	require.Len(t, later, 1)
	assert.Equal(t, before[0].Line+2, later[0].Line)
	text, ok, err := ToolResultAt(path, before[0].Line)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "AAA-1\n", text)
}
