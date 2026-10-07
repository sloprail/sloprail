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
// from a beforeReadFile between the Read's preToolUse and postToolUse (measured on
// cursor-agent 2026.10.01). A beforeReadFile is the Read's result ONLY while a Read of that
// file is pending: an attachment's, or the edit tool's, is not the Read's and is dropped.

type feeder struct {
	t   *testing.T
	rec harness.ToolResultRecorder
}

func newFeeder(t *testing.T) feeder {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	return feeder{t, New().(harness.ToolResultRecorder)}
}

func (f feeder) hook(raw string) {
	f.t.Helper()
	require.NoError(f.t, f.rec.RecordToolResult(New().ParseHook(strings.NewReader(raw))))
}

const common = `"conversation_id":"abc","session_id":"abc","workspace_roots":["/w"]`

func (f feeder) preRead(id string) {
	f.hook(`{"hook_event_name":"preToolUse",` + common + `,"tool_name":"Read","tool_input":{"file_path":"/w/a.txt"},"tool_use_id":"` + id + `"}`)
}

func (f feeder) beforeReadFile(content string) {
	f.hook(`{"hook_event_name":"beforeReadFile",` + common + `,"file_path":"/w/a.txt","content":"` + content + `"}`)
}

func (f feeder) postRead(id string) {
	f.hook(`{"hook_event_name":"postToolUse",` + common + `,"tool_name":"Read","tool_input":{"file_path":"/w/a.txt"},"tool_output":"{\"file_path\":\"/w/a.txt\",\"content_length\":11}","tool_use_id":"` + id + `"}`)
}

// readTranscript writes a transcript of n Read calls of /w/a.txt and returns it merged.
func readTranscript(t *testing.T, n int) []line {
	dir := filepath.Join(t.TempDir(), "agent-transcripts", "abc")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, "abc.jsonl")
	one := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/w/a.txt"}}]}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat(one, n)), 0o644))
	return openMerged(t, path)
}

func TestReadContentFromBeforeReadFileIsTheReadsResult(t *testing.T) {
	f := newFeeder(t)
	f.preRead("a")
	f.beforeReadFile("FIRST-BYTES")
	f.postRead("a")
	f.preRead("b")
	f.beforeReadFile("SECOND-BYTES")
	f.postRead("b")
	got := readTranscript(t, 2)
	require.Len(t, got, 4, "two Reads, each answered once")
	assert.Equal(t, "FIRST-BYTES", got[1].Message.Content[0].Content)
	assert.Equal(t, "SECOND-BYTES", got[3].Message.Content[0].Content)
}

// An attachment-style beforeReadFile (no Read pending) followed later by a real Read of the
// same path with different content: the Read gets its own content, never the attachment's.
func TestAnAttachmentsContentIsNeverALaterReadsResult(t *testing.T) {
	f := newFeeder(t)
	f.beforeReadFile("ATTACHMENT-BYTES") // @-attached file: no Read call
	f.preRead("a")
	f.beforeReadFile("READ-BYTES")
	f.postRead("a")
	got := readTranscript(t, 1)
	require.Len(t, got, 2)
	assert.Equal(t, "READ-BYTES", got[1].Message.Content[0].Content)
	for _, l := range got {
		for _, b := range l.Message.Content {
			assert.NotContains(t, b.Content, "ATTACHMENT")
		}
	}
}

// The edit tool reads the file it edits (a beforeReadFile with no Read pending): dropped, so a
// later Read does not inherit it, and a Read that returned nothing has no result.
func TestAnEditToolsReadIsDroppedAndAReadWithoutContentKeepsItsOwnOutput(t *testing.T) {
	f := newFeeder(t)
	f.preRead("a")
	f.postRead("a") // no beforeReadFile for this Read: only the length
	f.beforeReadFile("EDIT-TOOLS-READ")
	f.preRead("b")
	f.postRead("b")
	got := readTranscript(t, 2)
	require.Len(t, got, 4)
	for _, i := range []int{1, 3} {
		assert.Contains(t, got[i].Message.Content[0].Content, "content_length", "the hook's own output, not the edit tool's bytes")
	}
}

func TestAReadsContentWithNoPendingReadAtAllIsDropped(t *testing.T) {
	f := newFeeder(t)
	f.beforeReadFile("NOBODY-ASKED")
	got := readTranscript(t, 1)
	assert.Len(t, got, 1, "no result line")
}

func TestBeforeReadFileParsesAsAReadWithItsContent(t *testing.T) {
	in := New().ParseHook(strings.NewReader(`{"hook_event_name":"beforeReadFile",` + common + `,"file_path":"/w/a.txt","content":"hi\n"}`))
	assert.Equal(t, "Read", in.ToolName)
	assert.JSONEq(t, `{"file_path":"/w/a.txt"}`, string(in.ToolInput))
	require.NotNil(t, in.Result)
	assert.Equal(t, "hi\n", in.Result.Output)
}
