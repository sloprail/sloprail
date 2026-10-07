package cursor

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/harness/cursor/record"
)

// The hooks keep what Cursor reports of each call; the record the engine reads has the
// outcomes back as tool_result lines. Recorded inputs: shell-exit is harness-mocks
// runs/shell-exit-status (four Shell calls, three failing), file-tools-full is
// runs/file-tools (a Write that Cursor retried, a Read, a StrReplace).

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
	Line      int    `json:"sloprail_line"`
}

// replay feeds a recorded run's hooks, in order, into an isolated store and places its transcript where Cursor writes it.
func replay(t *testing.T, conversation, payloads, transcript string) string {
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
		if in.Event == string(SessionEnd) {
			continue
		}
		require.NoError(t, h.RecordToolResult(in))
	}
	require.NoError(t, sc.Err())
	body, err := os.ReadFile("testdata/" + transcript)
	require.NoError(t, err)
	return placeTranscript(t, conversation, string(body))
}

func placeTranscript(t *testing.T, conversation, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".cursor", "projects", "p", "agent-transcripts", conversation)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, conversation+".jsonl")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
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

// results are the tool_result blocks of the merged stream, keyed by the call they answer.
func results(ls []line) map[string]string {
	out := map[string]string{}
	for _, l := range ls {
		for _, b := range l.Message.Content {
			if b.Type == "tool_result" {
				out[b.ToolUseID] = b.Content
			}
		}
	}
	return out
}

func TestRecordedShellRunPairsEachOutcomeWithItsCall(t *testing.T) {
	path := replay(t, "76b9be4d-4628-44a4-8b98-cf3e32ee7a75", "shell-exit.payloads.jsonl", "shell-exit.transcript.jsonl")
	got := openMerged(t, path)
	require.Len(t, got, 7+4, "the 7 transcript lines and a result line after each of the four calls")

	call := func(i int) (id, name string) {
		b := got[i].Message.Content
		return b[len(b)-1].ID, b[len(b)-1].Name
	}
	id1, name := call(1)
	assert.Equal(t, "Bash", name, "Shell is the canonical Bash")
	assert.Equal(t, "cursor-L2-1", id1, "a tool_use gets an id from its physical line and place")

	res := results(got)
	require.Len(t, res, 4)
	assert.Equal(t, "Command failed with exit code 1", res["cursor-L2-1"], "postToolUseFailure's message")
	assert.Equal(t, "SOME-OUTPUT", res["cursor-L3-0"])
	assert.Equal(t, "MORE-OUTPUT\nERR-OUTPUT", res["cursor-L4-0"])
	assert.NotEmpty(t, res["cursor-L5-0"])

	// a failure is an error result, and the result line follows its call
	assert.True(t, got[2].Message.Content[0].IsError)
	assert.Equal(t, id1, got[2].Message.Content[0].ToolUseID)
	assert.NotEmpty(t, got[2].Timestamp)
}

func TestRecordedWriteAndEditToolsFireStepsOfTheirOwnUnderOneId(t *testing.T) {
	// runs/file-tools: Cursor's Write and StrReplace each fire a Read pre/post of their own
	// under the SAME tool_use_id as the Write that follows (the first a failing "File not
	// found"). Those steps are not the transcript's Read, and what they report is not the
	// Write's outcome: the Write, the Read (its bytes from beforeReadFile) and the edit each
	// get exactly their own result.
	path := replay(t, "c1cba5e7-b85d-487e-8b02-95381492cadb", "file-tools-full.payloads.jsonl", "file-tools.transcript.jsonl")
	res := results(openMerged(t, path))
	require.Len(t, res, 3)
	assert.Equal(t, `{"file_path":"<RUN>/note.txt","success":true}`, res["cursor-L2-1"], "the Write, not the failed Read that preceded it")
	assert.Equal(t, "hi\n", res["cursor-L3-0"], "the Read's bytes, from beforeReadFile")
	assert.Equal(t, `{"file_path":"<RUN>/note.txt","success":true}`, res["cursor-L4-0"], "the StrReplace, answered by the Write the hook saw")
}

// feeder drives the hooks by hand to make the situations a recording does not hold.
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

func (f feeder) pre(id, tool, input string) {
	f.hook(`{"hook_event_name":"preToolUse",` + common + `,"tool_name":"` + tool + `","tool_input":` + input + `,"tool_use_id":"` + id + `"}`)
}

func (f feeder) post(id, tool, input, output string) {
	out, _ := json.Marshal(output)
	f.hook(`{"hook_event_name":"postToolUse",` + common + `,"tool_name":"` + tool + `","tool_input":` + input + `,"tool_output":` + string(out) + `,"tool_use_id":"` + id + `"}`)
}

func shellPost(f feeder, id, cmd, text string) {
	env, _ := json.Marshal(map[string]any{"output": text, "exitCode": 0})
	f.post(id, "Shell", `{"command":"`+cmd+`","cwd":"","timeout":30000}`, string(env))
}

func shellPre(f feeder, id, cmd string) {
	f.pre(id, "Shell", `{"command":"`+cmd+`","cwd":"","timeout":30000}`)
}

// calls is a transcript of Shell calls, one per line.
func shellCalls(cmds ...string) string {
	var b strings.Builder
	for _, c := range cmds {
		b.WriteString(`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":{"command":"` + c + `","description":"d"}}]}}` + "\n")
	}
	return b.String()
}

func merged(t *testing.T, body string) map[string]string {
	return results(openMerged(t, placeTranscript(t, "abc", body)))
}

func TestOutOfOrderCompletionStillPairsByTheCallsOwnId(t *testing.T) {
	f := newFeeder(t)
	shellPre(f, "ua", "sleep 1")
	shellPre(f, "ub", "echo b")
	shellPost(f, "ub", "echo b", "B-OUT\n") // finished first
	shellPost(f, "ua", "sleep 1", "A-OUT\n")
	res := merged(t, shellCalls("sleep 1", "echo b"))
	assert.Equal(t, map[string]string{"cursor-L1-0": "A-OUT\n", "cursor-L2-0": "B-OUT\n"}, res)
}

func TestACallWhoseOutcomeNeverCameHasNoResultAndTheOthersKeepTheirs(t *testing.T) {
	f := newFeeder(t)
	shellPre(f, "u1", "echo one")
	shellPre(f, "u2", "echo two") // no post: the hook was lost, or the call is still running
	shellPre(f, "u3", "echo three")
	shellPost(f, "u1", "echo one", "ONE\n")
	shellPost(f, "u3", "echo three", "THREE\n")
	res := merged(t, shellCalls("echo one", "echo two", "echo three"))
	assert.Equal(t, map[string]string{"cursor-L1-0": "ONE\n", "cursor-L3-0": "THREE\n"}, res)
}

func TestIdenticalCommandsWithALostHookGetNoOutputOnTheWrongCall(t *testing.T) {
	// ls ran twice; the second call's whole pre/post pair was lost. One slot for two calls:
	// which call it belongs to is unknowable, so neither gets a result.
	f := newFeeder(t)
	shellPre(f, "u1", "ls")
	shellPost(f, "u1", "ls", "FIRST-LS\n")
	res := merged(t, shellCalls("ls", "ls"))
	assert.Empty(t, res)

	// Only the FIRST call's pre was lost: its post has no slot and is paired with nothing,
	// and the one slot cannot be placed against two calls.
	f = newFeeder(t)
	shellPost(f, "u1", "ls", "FIRST-LS\n")
	shellPre(f, "u2", "ls")
	shellPost(f, "u2", "ls", "SECOND-LS\n")
	res = merged(t, shellCalls("ls", "ls"))
	assert.Empty(t, res, "the second call's output is not put on the first")

	// Only the first call's POST was lost: the first never finished before the second began,
	// so start order cannot be proven to be transcript order: neither gets a result.
	f = newFeeder(t)
	shellPre(f, "u1", "ls")
	shellPre(f, "u2", "ls")
	shellPost(f, "u2", "ls", "SECOND-LS\n")
	res = merged(t, shellCalls("ls", "ls"))
	assert.Empty(t, res)
}

// Parallel identical commands: both started before either finished, so which transcript
// call is which start is unknowable and their outputs could swap; neither gets a result.
func TestParallelIdenticalCommandsGetNoResult(t *testing.T) {
	f := newFeeder(t)
	shellPre(f, "u1", "ls")
	shellPre(f, "u2", "ls")
	shellPost(f, "u2", "ls", "B\n")
	shellPost(f, "u1", "ls", "A\n")
	assert.Empty(t, merged(t, shellCalls("ls", "ls")))
	// sequential ones still pair
	f = newFeeder(t)
	shellPre(f, "u1", "ls")
	shellPost(f, "u1", "ls", "A\n")
	shellPre(f, "u2", "ls")
	shellPost(f, "u2", "ls", "B\n")
	assert.Equal(t, map[string]string{"cursor-L1-0": "A\n", "cursor-L2-0": "B\n"}, merged(t, shellCalls("ls", "ls")))
}

func TestARepeatedCallsResultsPairInTheOrderTheyStarted(t *testing.T) {
	f := newFeeder(t)
	for i, out := range []string{"one\n", "two\n"} {
		id := string(rune('a' + i))
		shellPre(f, id, "ls")
		shellPost(f, id, "ls", out)
		shellPost(f, id, "ls", out) // the same hook registered by a second source
	}
	res := merged(t, shellCalls("ls", "ls"))
	assert.Equal(t, map[string]string{"cursor-L1-0": "one\n", "cursor-L2-0": "two\n"}, res)
}

func TestAnInFlightCallThatIsNotInTheTranscriptYetDoesNotUnpairTheOthers(t *testing.T) {
	f := newFeeder(t)
	shellPre(f, "u1", "echo x")
	shellPost(f, "u1", "echo x", "X\n")
	shellPre(f, "u2", "echo x") // started, its line not written to the transcript yet, no outcome
	res := merged(t, shellCalls("echo x"))
	assert.Equal(t, map[string]string{"cursor-L1-0": "X\n"}, res)
}

func TestAnOutcomeWithNoCallInTheTranscriptMakesTheIdentityAmbiguous(t *testing.T) {
	f := newFeeder(t)
	shellPre(f, "u1", "echo x")
	shellPost(f, "u1", "echo x", "X\n")
	shellPre(f, "u2", "echo x")
	shellPost(f, "u2", "echo x", "X2\n")
	res := merged(t, shellCalls("echo x")) // two completed slots, one call
	assert.Empty(t, res)
}

func TestAToolWhoseInputIsSpeltDifferentlyInTheHookGetsNoResult(t *testing.T) {
	f := newFeeder(t)
	f.pre("g1", "Grep", `{"pattern":"a","file_path":"/w","output_mode":"files_with_matches"}`)
	f.post("g1", "Grep", `{"pattern":"a","file_path":"/w","output_mode":"files_with_matches"}`, "/w/x")
	// the transcript's Grep spells its arguments differently: no exact match, no result
	body := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Grep","input":{"pattern":"a","path":"/w"}}]}}` + "\n"
	assert.Empty(t, merged(t, body))

	// the same input on both sides does pair
	f = newFeeder(t)
	f.pre("g1", "Glob", `{"glob":"*.go"}`)
	f.post("g1", "Glob", `{"glob":"*.go"}`, "a.go")
	body = `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Glob","input":{"glob":"*.go"}}]}}` + "\n"
	assert.Equal(t, map[string]string{"cursor-L1-0": "a.go"}, merged(t, body))
}

func TestAnEditIsAWriteInTheHookAndPairsByFile(t *testing.T) {
	f := newFeeder(t)
	f.pre("w1", "Write", `{"file_path":"/w/a.txt","content":"FULL NEW CONTENT"}`)
	f.post("w1", "Write", `{"file_path":"/w/a.txt","content":"FULL NEW CONTENT"}`, `{"file_path":"/w/a.txt","success":true}`)
	body := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"StrReplace","input":{"path":"/w/a.txt","old_string":"a","new_string":"b"}}]}}` + "\n"
	assert.Equal(t, map[string]string{"cursor-L1-0": `{"file_path":"/w/a.txt","success":true}`}, merged(t, body))
}

func TestMergedLinesHaveNumbersOfTheirOwnThatDependOnTheStoreAlone(t *testing.T) {
	f := newFeeder(t)
	shellPre(f, "u1", "echo one")
	shellPost(f, "u1", "echo one", "ONE\n")
	path := placeTranscript(t, "abc", shellCalls("echo one"))
	first := openMerged(t, path)
	require.Len(t, first, 2)
	assert.Equal(t, record.ResultLineBase+2, first[1].Line, "result number 2 of the store: its post line")

	// A later call, its result, and a later transcript line change nothing already cited.
	shellPre(f, "u2", "echo two")
	shellPost(f, "u2", "echo two", "TWO\n")
	require.NoError(t, os.WriteFile(path, []byte(shellCalls("echo one", "echo two")), 0o644))
	second := openMerged(t, path)
	require.Len(t, second, 4)
	assert.Equal(t, record.ResultLineBase+2, second[1].Line)
	assert.Equal(t, record.ResultLineBase+4, second[3].Line)
}

func TestARecordWithNoStoredResultsIsTheTranscriptWithIds(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	got := openMerged(t, placeTranscript(t, "abc", shellCalls("ls")))
	require.Len(t, got, 1, "no result lines when nothing was recorded")
	assert.Equal(t, "cursor-L1-0", got[0].Message.Content[0].ID)
}

func TestRecordVersionMovesWhenALineIsAppended(t *testing.T) {
	f := newFeeder(t)
	path := placeTranscript(t, "abc", shellCalls("ls"))
	o := New().Transcripts().(harness.RecordOpener)
	size1, _, err := o.RecordVersion(path)
	require.NoError(t, err)
	shellPre(f, "u1", "ls")
	size2, _, err := o.RecordVersion(path)
	require.NoError(t, err)
	assert.Greater(t, size2, size1)
}

func TestAnOversizeOutputIsCutAndSaysSo(t *testing.T) {
	f := newFeeder(t)
	big := strings.Repeat("x", 1<<20+100)
	shellPre(f, "k", "big")
	shellPost(f, "k", "big", big)
	res := merged(t, shellCalls("big"))
	out := res["cursor-L1-0"]
	assert.Contains(t, out, "output cut at 1048576 of 1048676 bytes")
	assert.Less(t, len(out), len(big))
}

func TestTheStoreIsPrivate(t *testing.T) {
	f := newFeeder(t)
	shellPre(f, "u1", "ls")
	p, err := record.ToolResultsPath("abc")
	require.NoError(t, err)
	fi, err := os.Stat(p)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	di, err := os.Stat(filepath.Dir(p))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), di.Mode().Perm())
}

func TestAFullStoreRefusesRatherThanEvicting(t *testing.T) {
	f := newFeeder(t)
	shellPre(f, "u1", "ls")
	p, _ := record.ToolResultsPath("abc")
	require.NoError(t, os.Truncate(p, record.MaxStoreBytes))
	err := f.rec.RecordToolResult(harness.HookInput{Event: "preToolUse", SessionID: "abc", ToolUseID: "u2", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"ls"}`)})
	assert.ErrorIs(t, err, record.ErrStoreFull)
	fi, _ := os.Stat(p)
	assert.Equal(t, int64(record.MaxStoreBytes), fi.Size(), "nothing was written, nothing evicted")
}

func TestSessionEndKeepsTheConversationsFileForAResume(t *testing.T) {
	f := newFeeder(t)
	shellPre(f, "u1", "ls")
	p, _ := record.ToolResultsPath("abc")
	f.hook(`{"hook_event_name":"sessionEnd",` + common + `,"reason":"completed"}`)
	_, err := os.Stat(p)
	assert.NoError(t, err, "a resumed conversation keeps its earlier outputs citable")
}

func TestAnOldStoreWithALiveTranscriptSurvivesTheSweep(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	f := newFeeder(t)
	shellPre(f, "u1", "ls")
	p, _ := record.ToolResultsPath("abc")
	dir := filepath.Dir(p)
	aged := time.Now().Add(-record.TTL - time.Hour)
	tdir := filepath.Join(home, ".cursor", "projects", "w", "agent-transcripts", "resumed")
	require.NoError(t, os.MkdirAll(tdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tdir, "resumed.jsonl"), []byte("{}\n"), 0o644))
	for _, name := range []string{"resumed", "gone"} {
		old := filepath.Join(dir, name+".jsonl")
		require.NoError(t, os.WriteFile(old, []byte("{}\n"), 0o600))
		require.NoError(t, os.Chtimes(old, aged, aged))
	}
	require.NoError(t, os.Chtimes(filepath.Join(dir, ".swept"), aged, aged))
	shellPre(f, "u2", "ls")
	_, err := os.Stat(filepath.Join(dir, "resumed.jsonl"))
	assert.NoError(t, err, "no write for ages, but its transcript is live")
	_, err = os.Stat(filepath.Join(dir, "gone.jsonl"))
	assert.True(t, os.IsNotExist(err), "old store, no transcript: swept")
}

func TestOrphansOlderThanTheTTLAreSweptOnceADay(t *testing.T) {
	f := newFeeder(t)
	shellPre(f, "u1", "ls")
	dir, _ := record.ToolResultsPath("abc")
	dir = filepath.Dir(dir)
	old := filepath.Join(dir, "old-conversation.jsonl")
	require.NoError(t, os.WriteFile(old, []byte("{}\n"), 0o600))
	aged := time.Now().Add(-record.TTL - time.Hour)
	require.NoError(t, os.Chtimes(old, aged, aged))
	// the sweep already ran for today (the first append): the orphan stays until tomorrow
	shellPre(f, "u2", "ls")
	_, err := os.Stat(old)
	assert.NoError(t, err)
	// a day later it goes, and the live conversation's file stays
	marker := filepath.Join(dir, ".swept")
	yesterday := time.Now().Add(-25 * time.Hour)
	require.NoError(t, os.Chtimes(marker, yesterday, yesterday))
	shellPre(f, "u3", "ls")
	_, err = os.Stat(old)
	assert.True(t, os.IsNotExist(err), "swept")
	_, err = os.Stat(filepath.Join(dir, "abc.jsonl"))
	assert.NoError(t, err)
}

func TestParseHookPostToolUseCarriesTheOutput(t *testing.T) {
	ins := hookInputs(t, "shell-exit.payloads.jsonl")
	ok := inputOf(t, ins, "postToolUse", nil)
	require.NotNil(t, ok.Result)
	assert.False(t, ok.Result.IsError)
	assert.Equal(t, "Bash", ok.ToolName)
	fail := inputOf(t, ins, "postToolUseFailure", nil)
	require.NotNil(t, fail.Result)
	assert.True(t, fail.Result.IsError)
	assert.Equal(t, "Command failed with exit code 1", fail.Result.Output)
	assert.Nil(t, inputOf(t, ins, "preToolUse", nil).Result, "a pre-tool hook has no result")
	assert.NotEmpty(t, inputOf(t, ins, "preToolUse", nil).ToolUseID)
}

// Recorded (harness-mocks runs/subagent-transcripts): sessionStart fires for the session's
// own conversation only; the sub-agent's conversation gets tool hooks under its own id and
// nothing in a payload or a transcript names its parent. So a conversation without a
// sessionStart is not proven to be the session's own, and its "user" lines (the dispatch
// prompt) are not the user's words.
func TestSubagentConversationsUserLinesAreSidechain(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	h := New().(harness.ToolResultRecorder)
	f, err := os.Open("testdata/subagent-transcripts.payloads.jsonl")
	require.NoError(t, err)
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		in := New().ParseHook(strings.NewReader(sc.Text()))
		if in.Event == string(SessionEnd) {
			continue
		}
		require.NoError(t, h.RecordToolResult(in))
	}
	read := func(conv, file string) harness.Record {
		body, err := os.ReadFile("testdata/" + file)
		require.NoError(t, err)
		path := placeTranscript(t, conv, string(body))
		rc, err := New().Transcripts().(harness.RecordOpener).OpenRecord(path)
		require.NoError(t, err)
		defer rc.Close()
		br := bufio.NewReader(rc)
		b, err := br.ReadBytes('\n')
		require.NoError(t, err)
		r, err := New().Transcripts().ParseRecord(b[:len(b)-1])
		require.NoError(t, err)
		require.Equal(t, string(harness.EntryUser), r.Type)
		return r
	}
	assert.False(t, read("2d25ba87-93e7-4b09-a106-a597b00777e4", "subagent-transcripts.root.jsonl").IsSidechain, "the root's user line is the user's")
	assert.True(t, read("3ed4fb85-351b-40d8-ab3b-f564a04f219a", "subagent-transcripts.sub.jsonl").IsSidechain, "the sub-agent's is its dispatch prompt")
	assert.True(t, read("00000000-0000-0000-0000-00000000dead", "subagent-transcripts.root.jsonl").IsSidechain, "a conversation with no sessionStart is not proven the root: fail closed")
}
