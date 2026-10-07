package cursor

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/harness"
)

// The post-tool hook keeps each tool's output; the record the engine reads has them back
// as tool_result lines. Inputs are real recordings: shell-results is harness-mocks
// runs/additional-context (two Shell calls, the second failing), file-tools is
// runs/file-tools (a Write, a Read, a StrReplace); the payloads are their postToolUse /
// postToolUseFailure hook payloads, the transcripts their transcripts.

type line struct {
	Role    string `json:"role"`
	Message struct {
		Content []struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			Content   string          `json:"content"`
			IsError   bool            `json:"is_error"`
		} `json:"content"`
	} `json:"message"`
	Timestamp string `json:"timestamp"`
}

// recordedSession replays a run: the post-tool hooks in order into an isolated store,
// the transcript placed where Cursor writes it. It returns the path of that transcript.
func recordedSession(t *testing.T, conversation, payloads, transcript string) string {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	h := New().(harness.ToolResultRecorder)
	f, err := os.Open("testdata/" + payloads)
	require.NoError(t, err)
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		in := New().ParseHook(strings.NewReader(sc.Text()))
		if in.Result == nil {
			continue
		}
		require.NoError(t, h.RecordToolResult(in))
	}
	require.NoError(t, sc.Err())

	body, err := os.ReadFile("testdata/" + transcript)
	require.NoError(t, err)
	dir := filepath.Join(t.TempDir(), ".cursor", "projects", "p", "agent-transcripts", conversation)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, conversation+".jsonl")
	require.NoError(t, os.WriteFile(path, body, 0o644))
	return path
}

func openMerged(t *testing.T, path string) []line {
	t.Helper()
	rc, err := New().Transcripts().(harness.RecordOpener).OpenRecord(path)
	require.NoError(t, err)
	defer rc.Close()
	var out []line
	br := bufio.NewReader(rc)
	for {
		b, err := br.ReadBytes('\n')
		if len(strings.TrimSpace(string(b))) > 0 {
			var l line
			require.NoError(t, json.Unmarshal(b, &l), string(b))
			out = append(out, l)
		}
		if err == io.EOF {
			return out
		}
		require.NoError(t, err)
	}
}

func TestShellOutputsComeBackAsToolResultsRightAfterTheirCalls(t *testing.T) {
	path := recordedSession(t, "8e69a2cf-c829-44fd-a77a-2c4c7c7611c0", "shell-results.payloads.jsonl", "shell-results.transcript.jsonl")
	got := openMerged(t, path)
	require.Len(t, got, 7, "5 transcript lines and a result line after each of the two Shell calls")

	// user prompt, assistant (text+Shell), RESULT, assistant (Shell), RESULT, assistant (text), turn_ended
	assert.Equal(t, "user", got[0].Role)
	call1 := got[1].Message.Content[1]
	assert.Equal(t, "tool_use", call1.Type)
	assert.Equal(t, "Bash", call1.Name, "Shell is the canonical Bash")
	assert.Equal(t, "cursor-L2-1", call1.ID, "a tool_use gets an id from its physical line and place")

	res1 := got[2]
	assert.Equal(t, "user", res1.Role)
	assert.NotEmpty(t, res1.Timestamp)
	require.Len(t, res1.Message.Content, 1)
	assert.Equal(t, "tool_result", res1.Message.Content[0].Type)
	assert.Equal(t, call1.ID, res1.Message.Content[0].ToolUseID)
	assert.Equal(t, "FIRST\n", res1.Message.Content[0].Content, "a Shell's text, not its JSON envelope")
	assert.False(t, res1.Message.Content[0].IsError)

	call2 := got[3].Message.Content[0]
	assert.Equal(t, "cursor-L3-0", call2.ID)
	res2 := got[4].Message.Content[0]
	assert.Equal(t, call2.ID, res2.ToolUseID)
	assert.Equal(t, "Command failed with exit code 1", res2.Content, "postToolUseFailure's message")
	assert.True(t, res2.IsError)
}

func TestFileToolResultsMatchByFileEvenThoughAnEditIsAWriteInTheHook(t *testing.T) {
	path := recordedSession(t, "c1cba5e7-b85d-487e-8b02-95381492cadb", "file-tools.payloads.jsonl", "file-tools.transcript.jsonl")
	got := openMerged(t, path)

	var results []string
	for _, l := range got {
		for _, b := range l.Message.Content {
			if b.Type == "tool_result" {
				results = append(results, b.ToolUseID+" "+b.Content)
			}
		}
	}
	// The Write and the StrReplace (the hook saw a Write) have their outcomes. The Read's
	// result is the file's content from beforeReadFile, which fired twice in this run (the
	// edit tool reads the file too) and is one result.
	assert.Contains(t, strings.Join(results, "\n"), "cursor-L3-0 hi\n")
	assert.Len(t, results, 3)
	assert.Contains(t, strings.Join(results, "\n"), `cursor-L2-1 {"file_path":"<RUN>/note.txt","success":true}`)
	assert.Contains(t, strings.Join(results, "\n"), `cursor-L4-0 {"file_path":"<RUN>/note.txt","success":true}`, "the StrReplace is the canonical Edit, answered by the hook's Write")
}

func TestARecordWithNoStoredResultsIsTheTranscriptWithIds(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	body, err := os.ReadFile("testdata/shell-results.transcript.jsonl")
	require.NoError(t, err)
	dir := filepath.Join(t.TempDir(), "agent-transcripts", "abc")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, "abc.jsonl")
	require.NoError(t, os.WriteFile(path, body, 0o644))
	got := openMerged(t, path)
	assert.Len(t, got, 5, "no result lines when nothing was recorded")
	assert.Equal(t, "cursor-L2-1", got[1].Message.Content[1].ID)
}

func TestARepeatedCallsResultsPairInTheOrderTheyRan(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	rec := New().(harness.ToolResultRecorder)
	for i, out := range []string{"one\n", "two\n"} {
		require.NoError(t, rec.RecordToolResult(harness.HookInput{
			SessionID: "abc", ToolUseID: string(rune('a' + i)), ToolName: "Bash",
			ToolInput: json.RawMessage(`{"command":"ls"}`), Result: &harness.ToolResult{Output: out},
		}))
	}
	// The same hook run twice (two sources register it) is one result.
	require.NoError(t, rec.RecordToolResult(harness.HookInput{
		SessionID: "abc", ToolUseID: "b", ToolName: "Bash",
		ToolInput: json.RawMessage(`{"command":"ls"}`), Result: &harness.ToolResult{Output: "two\n"},
	}))

	dir := filepath.Join(t.TempDir(), "agent-transcripts", "abc")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, "abc.jsonl")
	tr := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":{"command":"ls"}}]}}` + "\n" +
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":{"command":"ls"}}]}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(tr), 0o644))

	got := openMerged(t, path)
	require.Len(t, got, 4)
	assert.Equal(t, "one\n", got[1].Message.Content[0].Content)
	assert.Equal(t, got[0].Message.Content[0].ID, got[1].Message.Content[0].ToolUseID)
	assert.Equal(t, "two\n", got[3].Message.Content[0].Content)
	assert.Equal(t, got[2].Message.Content[0].ID, got[3].Message.Content[0].ToolUseID)
	assert.NotEqual(t, got[0].Message.Content[0].ID, got[2].Message.Content[0].ID)
}

func TestRecordVersionMovesWhenAResultIsAppended(t *testing.T) {
	path := recordedSession(t, "8e69a2cf-c829-44fd-a77a-2c4c7c7611c0", "shell-results.payloads.jsonl", "shell-results.transcript.jsonl")
	o := New().Transcripts().(harness.RecordOpener)
	size1, _, err := o.RecordVersion(path)
	require.NoError(t, err)
	require.NoError(t, New().(harness.ToolResultRecorder).RecordToolResult(harness.HookInput{
		SessionID: "8e69a2cf-c829-44fd-a77a-2c4c7c7611c0", ToolUseID: "z", ToolName: "Bash",
		ToolInput: json.RawMessage(`{"command":"x"}`), Result: &harness.ToolResult{Output: "x"},
	}))
	size2, _, err := o.RecordVersion(path)
	require.NoError(t, err)
	assert.Greater(t, size2, size1)
}

func TestAnOversizeOutputIsCutAndSaysSo(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	big := strings.Repeat("x", 1<<20+100)
	require.NoError(t, New().(harness.ToolResultRecorder).RecordToolResult(harness.HookInput{
		SessionID: "abc", ToolUseID: "k", ToolName: "Bash",
		ToolInput: json.RawMessage(`{"command":"big"}`), Result: &harness.ToolResult{Output: big},
	}))
	dir := filepath.Join(t.TempDir(), "agent-transcripts", "abc")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, "abc.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":{"command":"big"}}]}}`+"\n"), 0o644))
	got := openMerged(t, path)
	require.Len(t, got, 2)
	out := got[1].Message.Content[0].Content
	assert.Contains(t, out, "output cut at 1048576 of 1048676 bytes")
	assert.Less(t, len(out), len(big))
}

func TestParseHookPostToolUseCarriesTheOutput(t *testing.T) {
	ins := hookInputs(t, "shell-results.payloads.jsonl")
	ok := inputOf(t, ins, "postToolUse", nil)
	require.NotNil(t, ok.Result)
	assert.Equal(t, "FIRST\n", ok.Result.Output)
	assert.False(t, ok.Result.IsError)
	assert.Equal(t, "Bash", ok.ToolName)
	fail := inputOf(t, ins, "postToolUseFailure", nil)
	require.NotNil(t, fail.Result)
	assert.True(t, fail.Result.IsError)
	assert.Equal(t, "Command failed with exit code 1", fail.Result.Output)
	assert.Nil(t, inputOf(t, hookInputs(t, "file-tools.payloads.jsonl"), "preToolUse", nil).Result, "a pre-tool hook has no result")
}
