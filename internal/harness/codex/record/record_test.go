package record

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toolUses are the tool_use blocks (name, input) the rollout's lines parse into.
func toolUses(t *testing.T, file string) (names []string, inputs []map[string]any) {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "testdata", file))
	require.NoError(t, err)
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		rec, err := Transcripts{}.ParseRecord(sc.Bytes())
		require.NoError(t, err)
		var msg struct {
			Content []struct {
				Type  string         `json:"type"`
				Name  string         `json:"name"`
				Input map[string]any `json:"input"`
			} `json:"content"`
		}
		if json.Unmarshal(rec.Message, &msg) != nil {
			continue
		}
		for _, b := range msg.Content {
			if b.Type == "tool_use" {
				names = append(names, b.Name)
				inputs = append(inputs, b.Input)
			}
		}
	}
	return names, inputs
}

// The real model (codex 0.160.1, ChatGPT login) runs every tool through a JS `exec`
// wrapper; a shell read of a SKILL.md reaches the record as a Bash command, which is
// what dispatch's skill-loaded check reads (testdata/real-model-exec.rollout.jsonl:
// the two exec calls of a real run, outputs dropped).
func TestParseRecord_RealModelExecWrapper(t *testing.T) {
	names, inputs := toolUses(t, "real-model-exec.rollout.jsonl")
	require.Equal(t, []string{"apply_patch", "Bash"}, names)

	assert.Equal(t, "*** Begin Patch\n*** Add File: /private/tmp/claude-501/real-proj/notes/hello.txt\n+hi\n*** End Patch", inputs[0]["command"])
	assert.Equal(t,
		"cat /private/tmp/claude-501/ch1/plugins/cache/sloprail-marketplace/sloprail/0.4.1/skills/authoring-guardrails/SKILL.md; cat .sloprail/file-guard/structure.yaml",
		inputs[1]["command"], "a shell read is a Bash command with the line the agent ran")
}

// What a hook fed the agent is a user-role message Codex wrote (recorded: harness-mocks
// codex-mock runs/stops), not the person's: it is meta, as Claude Code's Stop feedback is.
func TestParseRecord_HookPromptIsMeta(t *testing.T) {
	hook := `{"timestamp":"2026-10-01T12:43:30.000Z","ordinal":9,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<hook_prompt hook_run_id=\"stop\">REASON</hook_prompt>"}]}}`
	rec, err := Transcripts{}.ParseRecord([]byte(hook))
	require.NoError(t, err)
	assert.Equal(t, "user", rec.Type)
	assert.True(t, rec.IsMeta)

	person := `{"timestamp":"2026-10-01T12:43:30.000Z","ordinal":3,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the bug"}]}}`
	rec, err = Transcripts{}.ParseRecord([]byte(person))
	require.NoError(t, err)
	assert.False(t, rec.IsMeta)
}

func TestLocateRolloutAndConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	assert.Equal(t, home, ConfigDir())

	dir := filepath.Join(home, "sessions", "2026", "10", "07")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	want := filepath.Join(dir, "rollout-2026-10-07T16-24-50-abc-123.jsonl")
	require.NoError(t, os.WriteFile(want, []byte("{}\n"), 0o644))
	assert.Equal(t, want, FindRollout(home, "abc-123"))
	assert.Empty(t, FindRollout(home, "nope"))
}

// A sub-agent's rollout opens on a session_meta naming the ROOT session in session_id and
// its own thread in id (recorded: harness-mocks codex-mock nested-subagents). The record's
// origin is the thread's own, or the sub-agent would share its parent's identity and state.
func TestParseRecord_SessionMetaOriginIsTheThreadsOwnID(t *testing.T) {
	root, err := Transcripts{}.ParseRecord([]byte(
		`{"type":"session_meta","payload":{"id":"root-1","session_id":"root-1","thread_source":"user"}}`))
	require.NoError(t, err)
	sub, err := Transcripts{}.ParseRecord([]byte(
		`{"type":"session_meta","payload":{"id":"sub-1","session_id":"root-1","parent_thread_id":"root-1","thread_source":"subagent"}}`))
	require.NoError(t, err)

	assert.Equal(t, "root-1", root.UUID)
	assert.Equal(t, "sub-1", sub.UUID, "the sub-agent's origin is its own thread, not the root session")
	assert.Equal(t, "sub-1", sub.SessionID)
}
