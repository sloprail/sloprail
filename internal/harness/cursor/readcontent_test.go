package cursor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/harness"
)

// A Read's postToolUse carries only the file's length (recorded, harness-mocks
// runs/compaction-transcript-continuity: {"file_path","content_length"}); the bytes come
// from the beforeReadFile just before it (recorded, runs/file-tools), and the two are the
// one call, not two results.
func TestReadContentFromBeforeReadFileIsTheReadsResult(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	rec := New().(harness.ToolResultRecorder)
	feed := func(raw string) {
		t.Helper()
		require.NoError(t, rec.RecordToolResult(New().ParseHook(strings.NewReader(raw))))
	}
	for i, content := range []string{"FIRST-BYTES", "SECOND-BYTES"} {
		id := string(rune('a' + i))
		feed(`{"hook_event_name":"beforeReadFile","conversation_id":"abc","session_id":"abc","file_path":"/w/a.txt","content":"` + content + `","workspace_roots":["/w"]}`)
		feed(`{"hook_event_name":"postToolUse","conversation_id":"abc","session_id":"abc","tool_name":"Read","tool_input":{"file_path":"/w/a.txt"},"tool_output":"{\"file_path\":\"/w/a.txt\",\"content_length\":11}","tool_use_id":"` + id + `","workspace_roots":["/w"]}`)
	}
	dir := filepath.Join(t.TempDir(), "agent-transcripts", "abc")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, "abc.jsonl")
	read := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/w/a.txt"}}]}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(read+read), 0o644))
	got := openMerged(t, path)
	require.Len(t, got, 4, "two Reads, each answered once")
	assert.Equal(t, "FIRST-BYTES", got[1].Message.Content[0].Content)
	assert.Equal(t, "SECOND-BYTES", got[3].Message.Content[0].Content)
}

func TestBeforeReadFileParsesAsAReadWithItsContent(t *testing.T) {
	in := New().ParseHook(strings.NewReader(`{"hook_event_name":"beforeReadFile","conversation_id":"abc","file_path":"/w/a.txt","content":"hi\n","workspace_roots":["/w"]}`))
	assert.Equal(t, "Read", in.ToolName)
	assert.JSONEq(t, `{"file_path":"/w/a.txt"}`, string(in.ToolInput))
	require.NotNil(t, in.Result)
	assert.Equal(t, "hi\n", in.Result.Output)
}
