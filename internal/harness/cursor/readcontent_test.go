package cursor

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/harness/cursor/record"
)

// A Read's postToolUse carries only the file's length (recorded, harness-mocks
// runs/compaction-transcript-continuity: {"file_path","content_length"}); the bytes come
// from a beforeReadFile between the Read's preToolUse and postToolUse (recorded,
// runs/file-tools; measured on cursor-agent 2026.10.01). A beforeReadFile is a Read's
// result only while exactly one Read of that file is pending, and only once.

func (f feeder) preRead(id, gen string) {
	f.hook(`{"hook_event_name":"preToolUse",` + common + `,"generation_id":"` + gen + `","tool_name":"Read","tool_input":{"file_path":"/w/a.txt"},"tool_use_id":"` + id + `"}`)
}

func (f feeder) content(gen, bytes string) {
	f.hook(`{"hook_event_name":"beforeReadFile",` + common + `,"generation_id":"` + gen + `","file_path":"/w/a.txt","content":"` + bytes + `"}`)
}

func (f feeder) postRead(id, gen string) {
	f.hook(`{"hook_event_name":"postToolUse",` + common + `,"generation_id":"` + gen + `","tool_name":"Read","tool_input":{"file_path":"/w/a.txt"},"tool_output":"{\"file_path\":\"/w/a.txt\",\"content_length\":11}","tool_use_id":"` + id + `"}`)
}

func reads(t *testing.T, n int) map[string]string {
	one := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/w/a.txt"}}]}}` + "\n"
	return merged(t, strings.Repeat(one, n))
}

func TestReadContentFromBeforeReadFileIsTheReadsResult(t *testing.T) {
	f := newFeeder(t)
	f.preRead("a", "g1")
	f.content("g1", "FIRST-BYTES")
	f.postRead("a", "g1")
	f.preRead("b", "g1")
	f.content("g1", "SECOND-BYTES")
	f.postRead("b", "g1")
	assert.Equal(t, map[string]string{"cursor-L1-0": "FIRST-BYTES", "cursor-L2-0": "SECOND-BYTES"}, reads(t, 2))
}

// An attachment-style beforeReadFile (no Read pending) followed later by a real Read of the
// same path with different content: the Read gets its own content, never the attachment's.
func TestAnAttachmentsContentIsNeverALaterReadsResult(t *testing.T) {
	f := newFeeder(t)
	f.content("g1", "ATTACHMENT-BYTES") // @-attached file: no Read call
	f.preRead("a", "g1")
	f.content("g1", "READ-BYTES")
	f.postRead("a", "g1")
	assert.Equal(t, map[string]string{"cursor-L1-0": "READ-BYTES"}, reads(t, 1))
}

// The edit tool reads the file it edits (a beforeReadFile with no Read pending, after the
// Read's post closed it): dropped; a Read that returned no bytes keeps the hook's own output.
func TestAnEditToolsReadIsDroppedAndAReadWithoutContentKeepsItsOwnOutput(t *testing.T) {
	f := newFeeder(t)
	f.preRead("a", "g1")
	f.postRead("a", "g1")
	f.content("g1", "EDIT-TOOLS-READ")
	res := reads(t, 1)
	assert.Contains(t, res["cursor-L1-0"], "content_length")
	assert.NotContains(t, res["cursor-L1-0"], "EDIT-TOOLS")
}

func TestAReadsContentWithNoPendingReadAtAllIsDropped(t *testing.T) {
	f := newFeeder(t)
	f.content("g1", "NOBODY-ASKED")
	assert.Empty(t, reads(t, 1))
}

// A second content for one pending Read: which bytes the Read returned is unknowable, so
// it has no result at all (the first is never overwritten, the second never ignored).
func TestASecondContentForOnePendingReadMakesItAmbiguous(t *testing.T) {
	f := newFeeder(t)
	f.preRead("a", "g1")
	f.content("g1", "ONE")
	f.content("g1", "TWO")
	f.postRead("a", "g1")
	assert.Empty(t, reads(t, 1))
}

// Two Reads of one file pending at once and one content: which Read it belongs to is unknowable.
func TestOneContentWithTwoPendingReadsOfTheFileBelongsToNeither(t *testing.T) {
	f := newFeeder(t)
	f.preRead("a", "g1")
	f.preRead("b", "g1")
	f.content("g1", "BYTES")
	f.postRead("a", "g1")
	f.postRead("b", "g1")
	assert.Empty(t, reads(t, 2))
}

// A Read that never completed does not stay pending for ever: a content of the next
// generation binds to that generation's Read, not to the stale one.
func TestAPendingReadExpiresAtTheNextGeneration(t *testing.T) {
	f := newFeeder(t)
	f.preRead("a", "g1") // never posts
	f.preRead("b", "g2")
	f.content("g2", "G2-BYTES")
	f.postRead("b", "g2")
	assert.Equal(t, map[string]string{"cursor-L2-0": "G2-BYTES"}, reads(t, 2))
}

func TestAPendingReadExpiresAfterTheWindow(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	key := record.Identity("Read", []byte(`{"file_path":"/w/a.txt"}`))
	require.NoError(t, record.AppendLine("abc", record.StoredLine{Kind: record.KindPre, ToolUseID: "a", Tool: "Read", Key: key, Path: "/w/a.txt", At: old}))
	require.NoError(t, record.AppendLine("abc", record.StoredLine{Kind: record.KindContent, Path: "/w/a.txt", Output: "LATE-BYTES"}))
	assert.Empty(t, reads(t, 1), "the Read had been pending for an hour: the bytes are not its")
}

func TestBeforeReadFileParsesAsAReadWithItsContent(t *testing.T) {
	in := New().ParseHook(strings.NewReader(`{"hook_event_name":"beforeReadFile",` + common + `,"generation_id":"g1","file_path":"/w/a.txt","content":"hi\n"}`))
	assert.Equal(t, "Read", in.ToolName)
	assert.JSONEq(t, `{"file_path":"/w/a.txt"}`, string(in.ToolInput))
	assert.Equal(t, "g1", in.GenerationID)
	require.NotNil(t, in.Result)
	assert.Equal(t, "hi\n", in.Result.Output)
	var _ harness.ToolResultRecorder = New().(harness.ToolResultRecorder)
}
