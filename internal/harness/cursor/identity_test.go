package cursor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/transcript"
)

func TestMain(m *testing.M) {
	// This package's tests are the only ones in the process, so Cursor can be THE
	// harness here, which is what lets the neutral identity walk run on its layout.
	harness.Register(New())
	os.Exit(m.Run())
}

func TestConversationIDIsTheNameOfTheTranscriptFile(t *testing.T) {
	tr := New().Transcripts().(harness.ConversationNamer)
	assert.Equal(t, "c1cba5e7-b85d-487e-8b02-95381492cadb",
		tr.ConversationID("/h/.cursor/projects/p/agent-transcripts/c1cba5e7-b85d-487e-8b02-95381492cadb/c1cba5e7-b85d-487e-8b02-95381492cadb.jsonl"))
	assert.Equal(t, "", tr.ConversationID("/h/.claude/projects/p/abc.jsonl"))
	assert.Equal(t, "", tr.ConversationID("/h/.cursor/projects/p/agent-transcripts/a/b.jsonl"), "file and directory must agree")
}

// The identity of a conversation is its id, whatever the first line says: two
// conversations that open with the same prompt do not share one, and a transcript
// that does not exist yet still has one.
func TestIdentityIsTheConversationIDNotAFirstLineDigest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	line, err := os.ReadFile("testdata/file-tools.transcript.jsonl")
	require.NoError(t, err)
	mk := func(id string) string {
		p := filepath.Join(home, ".cursor", "projects", "p", "agent-transcripts", id, id+".jsonl")
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, line, 0o644))
		return p
	}
	a, b := mk("aaaaaaaa-0000-0000-0000-000000000001"), mk("bbbbbbbb-0000-0000-0000-000000000002")
	ia, err := transcript.StableSessionID("", a)
	require.NoError(t, err)
	ib, err := transcript.StableSessionID("", b)
	require.NoError(t, err)
	assert.Equal(t, "aaaaaaaa-0000-0000-0000-000000000001", ia)
	assert.Equal(t, "bbbbbbbb-0000-0000-0000-000000000002", ib, "same content, different conversations")

	missing := filepath.Join(home, ".cursor", "projects", "p", "agent-transcripts", "cccccccc-0", "cccccccc-0.jsonl")
	ic, err := transcript.StableSessionID("", missing)
	require.NoError(t, err, "a transcript Cursor has not written yet still has an identity")
	assert.Equal(t, "cccccccc-0", ic)
}

// A first hook names no transcript (recorded: transcript_path null at sessionStart and
// the first preToolUse); the path is the one Cursor will write.
func TestTranscriptFileIsDerivedWhenThePayloadNamesNone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	real, err := filepath.EvalSymlinks(ws)
	require.NoError(t, err)
	p := Payload{ConversationID: "conv-1", WorkspaceRoots: []string{ws}}
	want := filepath.Join(home, ".cursor", "projects", New().Transcripts().EncodeProjectDir(real), "agent-transcripts", "conv-1", "conv-1.jsonl")
	assert.Equal(t, want, p.TranscriptFile())
	_, statErr := os.Stat(want)
	assert.True(t, os.IsNotExist(statErr), "the file is not there yet: an empty transcript")

	named := "/named.jsonl"
	assert.Equal(t, named, Payload{TranscriptPath: &named, ConversationID: "x"}.TranscriptFile())
	assert.Equal(t, "", Payload{}.TranscriptFile())
}

func TestTaskCallIsRecognised(t *testing.T) {
	ps := fixture(t, "subagent.payloads.jsonl")
	task := find(t, ps, PreToolUse, func(p Payload) bool { return p.ToolName == "Task" })
	typ, desc, ok := task.Task()
	require.True(t, ok)
	assert.Equal(t, "shell", typ)
	assert.Equal(t, "Echo SUB-DONE only", desc)
	sh := find(t, ps, PreToolUse, func(p Payload) bool { return p.ToolName == "Shell" })
	_, _, ok = sh.Task()
	assert.False(t, ok)
}

func TestJudgeGrantRefusesWritesOutsideItAndInsideReadonly(t *testing.T) {
	proj, ans := t.TempDir(), t.TempDir()
	g := JudgeGrant{Writable: []string{ans}, Readonly: []string{proj}}
	write := func(path string) Payload {
		return Payload{HookEventName: PreToolUse, ToolName: "Write", ToolInput: []byte(`{"file_path":"` + path + `","content":"x"}`)}
	}
	assert.Equal(t, "", g.Refusal(write(filepath.Join(ans, "answer.json"))), "the answer file is writable, though it does not exist yet")
	assert.Contains(t, g.Refusal(write(filepath.Join(proj, "x.txt"))), "may read")
	assert.Contains(t, g.Refusal(write(filepath.Join(os.TempDir(), "elsewhere-xyz.txt"))), "only its answer file")
	assert.Equal(t, "", g.Refusal(Payload{HookEventName: PreToolUse, ToolName: "Read", ToolInput: []byte(`{"file_path":"/etc/hosts"}`)}), "reads are not this layer's")
	assert.Contains(t, g.Refusal(Payload{HookEventName: PreToolUse, ToolName: "Write", ToolInput: []byte(`{}`)}), "not named")

	env := g.Encode()
	got, ok := ParseJudgeGrant(func(k string) string {
		if k == JudgeGrantEnv {
			return env[len(JudgeGrantEnv)+1:]
		}
		return ""
	})
	require.True(t, ok)
	assert.Equal(t, g, got)
	_, ok = ParseJudgeGrant(func(string) string { return "" })
	assert.False(t, ok, "not a launched judge")
	bad, ok := ParseJudgeGrant(func(string) string { return "{not json" })
	assert.True(t, ok)
	assert.Contains(t, bad.Refusal(write(filepath.Join(ans, "a"))), "only its answer file", "an unreadable grant allows nothing")
}

func TestResolveFindsTheRunningPluginFromItsEnvironment(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".cursor-plugin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".cursor-plugin", "plugin.json"), []byte(`{"name":"sloprail"}`), 0o644))
	t.Setenv(PluginRootEnv, root)
	res, err := New().ResolvePlugins("/proj", t.TempDir())
	require.NoError(t, err)
	require.Len(t, res.Roots, 1)
	assert.Equal(t, "sloprail", res.Roots[0].Plugin.Name)
	assert.Equal(t, root, res.Roots[0].Dir)
}
