package codex

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/harness"
)

// payloads reads a recorded run's hook payloads (testdata/SOURCE.txt), with the
// recording's <RUN> placeholder replaced by dir.
func payloads(t *testing.T, name, dir string) []harness.HookInput {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	var out []harness.HookInput
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		out = append(out, New().ParseHook(strings.NewReader(strings.ReplaceAll(line, "<RUN>", dir))))
	}
	return out
}

func TestParseHook_FileToolsRun(t *testing.T) {
	dir := t.TempDir()
	// the file the recorded second patch updates
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("HELLO-FILE\n"), 0o644))
	in := payloads(t, "file-tools.payloads.jsonl", dir)
	require.Len(t, in, 9)

	bash := in[0]
	assert.Equal(t, "PreToolUse", bash.Event)
	assert.Equal(t, "Bash", bash.ToolName, "Codex's shell tool is already the canonical Bash")
	assert.JSONEq(t, `{"command":"test -f ./nothere.txt && sed -n '1,200p' ./nothere.txt || true"}`, string(bash.ToolInput))
	assert.Equal(t, "01a1023b-6884-70e1-97e5-09a47aeb4af5", bash.SessionID)
	assert.Equal(t, dir, bash.Cwd)
	assert.Equal(t, "exec-77527327-a41c-4e43-af52-24773b0d7dc3", bash.ToolUseID)
	assert.Contains(t, bash.TranscriptPath, "rollout-2026-10-03T16-46-51-01a1023b-6884-70e1-97e5-09a47aeb4af5.jsonl")
	assert.False(t, bash.IsSubagent())
	assert.Empty(t, bash.Files)

	add := in[2]
	assert.Equal(t, "apply_patch", add.ToolName)
	assert.Equal(t, []harness.FileEffect{{
		Kind: harness.FileCreate, Path: filepath.Join(dir, "hello.txt"), NewContent: "HELLO-FILE\n", ResultKnown: true,
	}}, add.Files, "an Add File patch is a create with the stated body")

	assert.Empty(t, in[3].Files, "the file effects are the PRE hook's: after the call the tree diff reports what landed")

	update := in[6]
	assert.Equal(t, []harness.FileEffect{{
		Kind: harness.FileUpdate, Path: filepath.Join(dir, "hello.txt"), NewContent: "HELLO-AGAIN\n", ResultKnown: true,
	}}, update.Files, "an Update File patch is applied to the file as it stands")

	stop := in[8]
	assert.Equal(t, "Stop", stop.Event)
	assert.False(t, stop.StopHookActive)
}

func TestParseHook_APatchThatFindsNoPlaceHasAnUnknownResult(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("SOMETHING ELSE\n"), 0o644))
	in := payloads(t, "file-tools.payloads.jsonl", dir)

	fx := in[6].Files
	require.Len(t, fx, 1)
	assert.Equal(t, harness.FileUpdate, fx[0].Kind)
	assert.False(t, fx[0].ResultKnown, "the write is real and its path known; its bytes are not")
}

func TestParseHook_SubagentHooksNameTheSubagentsOwnRecord(t *testing.T) {
	in := payloads(t, "background-agent.payloads.jsonl", "/run")
	var parent, sub *harness.HookInput
	for i := range in {
		switch {
		case in[i].AgentID == "" && in[i].Event == "PreToolUse" && in[i].ToolName == "Bash":
			parent = &in[i]
		case in[i].AgentID != "" && in[i].Event == "PreToolUse":
			sub = &in[i]
		}
	}
	require.NotNil(t, parent)
	require.NotNil(t, sub)

	assert.Contains(t, parent.TranscriptPath, "01a1023f-79c6-7c03-b8be-706469caa6fe")
	assert.Empty(t, parent.AgentTranscriptPath)

	assert.True(t, sub.IsSubagent())
	assert.Equal(t, "01a1023f-a7bb-7a21-87f6-210abb3754f3", sub.AgentID)
	assert.Equal(t, "default", sub.AgentType)
	assert.Equal(t, "01a1023f-79c6-7c03-b8be-706469caa6fe", sub.SessionID, "session_id stays the parent's")
	assert.Contains(t, sub.AgentTranscriptPath, "01a1023f-a7bb-7a21-87f6-210abb3754f3",
		"a sub-agent's transcript_path IS its own rollout")
	assert.Empty(t, sub.TranscriptPath, "the parent's record is not named: LocateTranscript finds it by session id")
}

func TestParseHook_SubagentStopCarriesBothRecords(t *testing.T) {
	in := payloads(t, "subagent-lifecycle.payloads.jsonl", "/run")
	require.Len(t, in, 3)
	assert.Equal(t, "SubagentStart", in[0].Event)
	assert.True(t, in[0].IsSubagent())
	assert.Equal(t, "default", in[0].AgentType)

	stop := in[1]
	assert.Equal(t, "SubagentStop", stop.Event)
	assert.Contains(t, stop.TranscriptPath, "01a10255-45a3-7780-b91f-9c1e276138d7", "the parent's")
	assert.Contains(t, stop.AgentTranscriptPath, "01a10255-70ff-7251-8978-9d630b4e8be8", "the sub-agent's")
}

func TestParseHook_ANullTranscriptPathIsNoRecord(t *testing.T) {
	// recorded: runs/ephemeral-no-transcript — an ephemeral session keeps none
	got := New().ParseHook(strings.NewReader(`{"session_id":"s","transcript_path":null,"cwd":"/c","hook_event_name":"SessionStart","source":"startup"}`))
	assert.Empty(t, got.TranscriptPath)
	assert.Equal(t, "startup", got.Source)
}

func TestParseHook_EmptyOrUnreadableIsAZeroInput(t *testing.T) {
	assert.Equal(t, harness.HookInput{}, New().ParseHook(strings.NewReader("")))
	assert.Equal(t, harness.HookInput{}, New().ParseHook(strings.NewReader("{")))
}

func TestDetect_YieldsToClaudeCodeMarkers(t *testing.T) {
	assert.True(t, Detect([]string{"CODEX_THREAD_ID=t"}))
	assert.True(t, Detect([]string{"PLUGIN_ROOT=/p"}))
	assert.False(t, Detect([]string{"CODEX_HOME=/h"}), "a directory a user may export for any reason")
	for _, claude := range []string{"CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "CLAUDE_CODE_SESSION_ID=s"} {
		assert.False(t, Detect([]string{"CODEX_THREAD_ID=t", claude}), claude+": both marker sets is ambiguous, Claude Code wins")
	}
	assert.True(t, Detect([]string{"CODEX_THREAD_ID=t", "CLAUDECODE="}), "an empty marker is not one")
}

// Spawning a sub-agent is Agent with a prompt, whatever the harness calls it; the
// recorded spawn_agent call (testdata/background-agent.payloads.jsonl) is
// {"message": ...}.
func TestParseHook_SpawnAgentIsTheCanonicalAgent(t *testing.T) {
	in := payloads(t, "background-agent.payloads.jsonl", "/run")
	var spawn *harness.HookInput
	for i := range in {
		if in[i].Event == "PreToolUse" && in[i].NativeToolName == "spawn_agent" {
			spawn = &in[i]
		}
	}
	require.NotNil(t, spawn, "the recording holds a spawn_agent PreToolUse")
	assert.Equal(t, "Agent", spawn.ToolName)
	assert.Equal(t, "spawn_agent", spawn.NativeTool())
	var args map[string]any
	require.NoError(t, json.Unmarshal(spawn.ToolInput, &args))
	assert.Contains(t, args, "prompt", "the message is the canonical prompt")
	assert.NotContains(t, args, "message")

	for _, h := range in {
		if h.ToolName == "Bash" {
			assert.Empty(t, h.NativeToolName, "Codex's shell tool is already Bash")
		}
	}
}

func TestCurrentSessionPath_IsTheRolloutOfTheThreadInTheEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	dir := filepath.Join(home, "sessions", "2026", "10", "07")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	rollout := filepath.Join(dir, "rollout-2026-10-07T10-00-00-thread-1.jsonl")
	require.NoError(t, os.WriteFile(rollout, []byte("{}\n"), 0o644))

	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	h := Harness{}
	assert.Equal(t, rollout, h.CurrentSessionPath("/any", env(map[string]string{"CODEX_THREAD_ID": "thread-1"})))
	assert.Equal(t, rollout, h.CurrentSessionPath("/any", env(map[string]string{"CODEX_SESSION_ID": "thread-1"})))
	assert.Empty(t, h.CurrentSessionPath("/any", env(map[string]string{"CODEX_THREAD_ID": "other"})))
	assert.Empty(t, h.CurrentSessionPath("/any", env(nil)))
}

func TestProjectSkillDirs_CodexReadsAgentsSkills(t *testing.T) {
	assert.Equal(t, []string{".agents/skills"}, harness.ProjectSkillDirs(New()))
}

func TestRenderHook_IsTheContractCodexDocuments(t *testing.T) {
	render := func(r harness.HookResponse) string {
		var b bytes.Buffer
		require.NoError(t, New().RenderHook(&b, r))
		return b.String()
	}
	assert.Equal(t,
		`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"no"}}`+"\n",
		render(harness.HookResponse{Decision: harness.Deny, Reason: "no"}))
	assert.Equal(t, `{"decision":"block","reason":"again"}`+"\n",
		render(harness.HookResponse{Decision: harness.Block, Reason: "again"}))
	assert.Equal(t, `{"hookSpecificOutput":{"additionalContext":"c","hookEventName":"SessionStart"}}`+"\n",
		render(harness.HookResponse{Event: "SessionStart", AdditionalContext: "c"}))
	assert.Empty(t, render(harness.HookResponse{}))
	// A PreToolUse hook printing `continue`, or any decision but block, FAILS in Codex
	// (harness-mocks codex-mock/internal/hooks/decide.go preToolOutput); never emitted.
	assert.NotContains(t, render(harness.HookResponse{Decision: harness.Deny, Reason: "x"}), "continue")
}
