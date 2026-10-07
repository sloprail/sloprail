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
	require.NoError(t, rec.RecordToolResult(harness.HookInput{
		SessionID: id, ToolUseID: "t1", ToolName: "Bash",
		ToolInput: json.RawMessage(`{"command":"echo MARKER-7f3a"}`),
		Result:    &harness.ToolResult{Output: "MARKER-7f3a\n"},
	}))

	got, err = CiteWithSources(path, "MARKER-7f3a", []SourceType{SourceToolResult})
	require.NoError(t, err)
	require.Len(t, got, 1, "the recorded output grounds the quote")
	assert.Equal(t, 3, got[0].Line, "the result line follows the tool_use line of the merged stream")

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
