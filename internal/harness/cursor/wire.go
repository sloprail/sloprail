package cursor

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/harness/cursor/record"
)

func init() { harness.Register(New()) }

// Cursor's tool names, in the canonical vocabulary (harness.Tool*): Shell is Bash;
// Write and Read already match ({file_path, content} / {file_path}). An edit reaches
// preToolUse as a Write carrying the whole new content (recorded), so there is no
// Edit to map. Delete is {file_path} (recorded, cursor-agent 2026.10.01) and has no
// canonical argument shape, so it is reported as a file effect.
const (
	toolShell  = "Shell"
	toolDelete = "Delete"
)

// ParseHook implements harness.HookWire.
//
// transcript_path is the payload's own only when it names one: null at sessionStart
// and the first events, where the engine asks LocateTranscript instead. The session
// id is Cursor's session_id (equal to conversation_id in every recording), and Cwd is
// the project folder (Payload.Folder), because Cursor's own cwd is empty or a URI.
func (Harness) ParseHook(r io.Reader) harness.HookInput {
	p := ReadHook(r)
	in := harness.HookInput{
		Event:               string(p.HookEventName),
		SessionID:           firstNonEmpty(p.SessionID, p.ConversationID),
		GenerationID:        p.GenerationID,
		TranscriptPath:      p.Transcript(),
		AgentTranscriptPath: p.AgentTranscriptPath,
		AgentType:           p.SubagentType,
		Cwd:                 p.Folder(),
		ToolName:            p.ToolName,
		ToolUseID:           p.ToolUseID,
		ToolInput:           p.ToolInput,
		StopHookActive:      p.HookEventName == Stop && p.LoopCount > 0,
	}
	switch p.ToolName {
	case toolShell:
		in.ToolName = harness.ToolBash
	case toolDelete:
		var d struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal(p.ToolInput, &d) == nil && d.FilePath != "" {
			in.Files = []harness.FileEffect{{Kind: harness.FileDelete, Path: d.FilePath}}
		}
	}
	switch p.HookEventName {
	case PostToolUse:
		in.Result = &harness.ToolResult{Output: toolOutputText(p.ToolName, p.ToolOutput)}
	case PostToolUseFailure:
		in.Result = &harness.ToolResult{Output: p.ErrorMessage, IsError: true}
	case BeforeReadFile:
		// A Read's postToolUse output is only {file_path, content_length}; the bytes are in
		// this hook. It is reported as the Read's result and RecordToolResult tells it apart
		// by the event.
		in.ToolName = harness.ToolRead
		in.ToolInput, _ = json.Marshal(map[string]string{"file_path": p.FilePath})
		in.Result = &harness.ToolResult{Output: p.Content}
	}
	return in
}

// toolOutputText is a postToolUse's output as the text the tool printed. A Shell's is
// {"output","exitCode"} (recorded) and the text is "output"; every other tool's is its
// own JSON document (Write {"file_path","success"}, Read {"file_path","content_length"}),
// kept as it came.
func toolOutputText(tool, raw string) string {
	if tool != toolShell {
		return raw
	}
	var o struct {
		Output *string `json:"output"`
	}
	if json.Unmarshal([]byte(raw), &o) != nil || o.Output == nil {
		return raw
	}
	return *o.Output
}

// RecordToolResult implements harness.ToolResultRecorder: Cursor's transcript holds no
// tool_result, so what its hooks report of each call is kept in sloprail's own store, from
// which the record the engine reads is merged (record.OpenRecord, which says how a result
// is paired with its call).
//
//   - preToolUse: the call's slot (its tool_use_id and Identity), for every tool.
//   - postToolUse / postToolUseFailure: the call's outcome, by tool_use_id.
//   - beforeReadFile: the bytes of a file a Read is about to return.
func (Harness) RecordToolResult(in harness.HookInput) error {
	switch in.Event {
	case string(SessionStart):
		// the session's own conversation (see record.KindRoot): never fired for a sub-agent's
		return record.AppendLine(in.SessionID, record.StoredLine{Kind: record.KindRoot})
	case string(PreToolUse):
		if in.ToolUseID == "" || in.ToolName == "" {
			return nil
		}
		l := record.StoredLine{
			Kind: record.KindPre, ToolUseID: in.ToolUseID, Tool: in.ToolName,
			Key: record.Identity(in.ToolName, in.ToolInput), Generation: in.GenerationID,
		}
		if in.ToolName == harness.ToolRead {
			l.Path = filePathOf(in.ToolInput)
		}
		return record.AppendLine(in.SessionID, l)
	case string(PostToolUse), string(PostToolUseFailure):
		if in.Result == nil || in.ToolUseID == "" {
			return nil
		}
		return record.AppendLine(in.SessionID, record.StoredLine{
			Kind: record.KindPost, ToolUseID: in.ToolUseID, Tool: in.ToolName,
			Generation: in.GenerationID, Output: in.Result.Output, IsError: in.Result.IsError,
		})
	case string(BeforeReadFile):
		if in.Result == nil {
			return nil
		}
		return record.AppendLine(in.SessionID, record.StoredLine{
			Kind: record.KindContent, Path: filePathOf(in.ToolInput),
			Generation: in.GenerationID, Output: in.Result.Output,
		})
	}
	return nil
}

func filePathOf(input json.RawMessage) string {
	var in struct {
		FilePath string `json:"file_path"`
	}
	_ = json.Unmarshal(input, &in)
	return in.FilePath
}

// RenderHook implements harness.HookWire: Cursor's hook output.
//
// A Deny is {"permission":"deny", user_message, agent_message} (the reason in both:
// recordings show only user_message reaching the run, the doc says agent_message is
// the agent's). A Block is {"followup_message"}, the next prompt of the conversation,
// which Cursor's stop and subagentStop honour (interactive only: `-p` never fires
// stop). Context is {"additional_context"}. Cursor has no field for a note shown to
// the person on an allow, so a SystemMessage with no refusal is not sent (the engine
// also writes it to stderr); on a Deny it rides in user_message. An Allow with
// nothing to say writes nothing.
// sr:invariant gates/refusal-stops-the-action
// sr:invariant gates/stop-refusal-continues-the-turn
func (Harness) RenderHook(w io.Writer, resp harness.HookResponse) error {
	out := map[string]any{}
	switch resp.Decision {
	case harness.Deny:
		out["permission"] = "deny"
		out["user_message"] = resp.Reason
		out["agent_message"] = resp.Reason
	case harness.Block:
		out["followup_message"] = resp.Reason
	}
	if resp.AdditionalContext != "" {
		out["additional_context"] = resp.AdditionalContext
	}
	if len(out) == 0 {
		return nil
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(out); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// LocateTranscript implements harness.TranscriptLocator: the file Cursor writes the
// conversation to, derived from the project folder and conversation id, when the
// payload names none. The file may not exist yet.
func (Harness) LocateTranscript(in harness.HookInput) string {
	return record.TranscriptPath(record.ConfigDir(), in.Cwd, in.SessionID)
}

// Detect implements harness.Detector: the variables cursor-agent sets in its own
// shell tool and hooks. CURSOR_VERSION and CURSOR_PROJECT_DIR are left out on
// purpose, since an editor terminal can carry them with no cursor-agent running; the
// plugin's hook wrapper names the harness outright (harness.SelectEnv) instead.
func (Harness) Detect(environ []string) bool {
	found := false
	for _, kv := range environ {
		key, val, _ := strings.Cut(kv, "=")
		if val == "" {
			continue
		}
		switch key {
		case "CURSOR_AGENT", "CURSOR_INVOKED_AS", PluginRootEnv:
			found = true
		case "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SESSION_ID":
			// A Claude Code session is Claude's, wherever it was started (inside a
			// Cursor agent's shell the agent's variables are inherited too).
			return false
		}
	}
	return found
}

// ChildEnvBlocklist implements harness.ChildEnvBlocklist: the enclosing agent's
// conversation, request and transcript, set in every hook and shell tool
// (harness-mocks runs/subprocess-session-env). CURSOR_AGENT and CURSOR_INVOKED_AS stay,
// as CLAUDECODE does.
func (Harness) ChildEnvBlocklist() []string {
	return []string{"CURSOR_CONVERSATION_ID", "CURSOR_REQUEST_ID", "CURSOR_TRANSCRIPT_PATH"}
}

// JudgeRefusal implements harness.JudgeGate: under a judge sr-agent launched
// (SLOPRAIL_JUDGE_GRANT), a file change outside what it was granted is refused.
func (Harness) JudgeRefusal(in harness.HookInput, getenv func(string) string) string {
	g, ok := ParseJudgeGrant(getenv)
	if !ok || in.Event != string(PreToolUse) {
		return ""
	}
	return g.RefusalFor(in.ToolName, in.ToolInput, in.Cwd)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
