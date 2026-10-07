package cursor

import (
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/sloprail/sloprail/internal/harness/cursor/record"
)

// Event is Cursor's hook_event_name. Cursor spells them camelCase, unlike Claude's
// PascalCase, and has more of them; only those sloprail binds are named here.
//
// Every name below appears in a recorded payload in the harness-mocks repository
// (cursor-mock/snapshots/runs/{file-tools,pretool-refusal,subagent-lifecycle-hooks}),
// except subagentStart/subagentStop, which the hooks doc lists
// (https://cursor.com/docs/hooks#subagentstart) and which the recordings show NOT
// firing in print mode (capability hook-common-payload, cursor deviations).
type Event string

const (
	SessionStart         Event = "sessionStart"
	SessionEnd           Event = "sessionEnd"
	PreToolUse           Event = "preToolUse"
	PostToolUse          Event = "postToolUse"
	PostToolUseFailure   Event = "postToolUseFailure"
	BeforeShellExecution Event = "beforeShellExecution"
	AfterShellExecution  Event = "afterShellExecution"
	BeforeReadFile       Event = "beforeReadFile"
	AfterFileEdit        Event = "afterFileEdit"
	SubagentStart        Event = "subagentStart"
	SubagentStop         Event = "subagentStop"
	Stop                 Event = "stop"
	PreCompact           Event = "preCompact"
)

// HookPoint is the sr-session subcommand an event is handled by ("start",
// "pre-tool", "stop", "subagent-start", "subagent-stop"), or "" for an event
// sloprail binds nothing to.
//
// beforeShellExecution is deliberately not "pre-tool": every Shell call raises
// preToolUse first (recorded), so binding both would judge each command twice.
// preToolUse covers every tool, shell included.
func (e Event) HookPoint() string {
	switch e {
	case SessionStart:
		return "start"
	case PreToolUse:
		return "pre-tool"
	case Stop:
		return "stop"
	case SubagentStart:
		return "subagent-start"
	case SubagentStop:
		return "subagent-stop"
	case PreCompact:
		return "post-tool"
	}
	return ""
}

// Payload is what Cursor puts on a hook's standard input: a snapshot of the
// recorded payloads, not a published contract. The common fields are on every
// event; the rest depend on the event.
type Payload struct {
	HookEventName Event  `json:"hook_event_name"`
	CursorVersion string `json:"cursor_version"`

	ConversationID string `json:"conversation_id"`
	GenerationID   string `json:"generation_id"`
	SessionID      string `json:"session_id"`

	// WorkspaceRoots is the project's root folders. Print mode names one even
	// with --add-dir (recorded: runs/multiroot-workspace).
	WorkspaceRoots []string `json:"workspace_roots"`

	// TranscriptPath is null at sessionStart and at the first events of a
	// conversation, until its file exists (recorded), so "" is an expected value
	// and not a fault.
	TranscriptPath *string `json:"transcript_path"`

	// Cwd is carried only by Shell events, and unreliable: recorded as "" (the
	// command having run from the project root), and file:// URIs have been seen.
	// Use Folder, not this.
	Cwd string `json:"cwd"`

	ToolName  string          `json:"tool_name"`
	ToolUseID string          `json:"tool_use_id"`
	ToolInput json.RawMessage `json:"tool_input"`

	// ToolOutput is postToolUse's result: a JSON document encoded as a string.
	ToolOutput string `json:"tool_output"`

	// Command is beforeShellExecution's / afterShellExecution's command line.
	Command string `json:"command"`

	// Content is beforeReadFile's: the file's bytes as the agent is about to read them
	// (recorded: runs/file-tools). It is the only hook that carries a Read's content.
	Content string `json:"content"`

	// FilePath and Edits are afterFileEdit's (and FilePath beforeReadFile's): the file and each {old_string,
	// new_string} applied.
	FilePath string `json:"file_path"`
	Edits    []Edit `json:"edits"`

	// ErrorMessage and FailureType are postToolUseFailure's; a refusal by a hook is
	// failure_type "permission_denied" with the hook's user_message as the error.
	ErrorMessage string `json:"error_message"`
	FailureType  string `json:"failure_type"`

	IsBackgroundAgent bool `json:"is_background_agent"`

	// Status and LoopCount are the stop event's (TUI only: `-p` never fires it): how
	// the turn ended, and how many follow-up re-prompts this turn has had already
	// (followup_message re-prompts, loop_count 0 then 1).
	Status    string `json:"status"`
	LoopCount int    `json:"loop_count"`

	// Subagent fields, documented but never seen in print mode.
	SubagentType         string `json:"subagent_type"`
	ParentConversationID string `json:"parent_conversation_id"`
	AgentTranscriptPath  string `json:"agent_transcript_path"`
}

// Edit is one replacement of an afterFileEdit.
type Edit struct {
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// ReadHook reads the hook payload from r. An unreadable or empty body yields a
// zero payload, as the Claude Code reader does: a hook that cannot read its input
// has nothing to judge.
func ReadHook(r io.Reader) Payload {
	var p Payload
	b, err := io.ReadAll(r)
	if err != nil || len(b) == 0 {
		return p
	}
	_ = json.Unmarshal(b, &p)
	return p
}

// IsCursorPayload reports whether a raw hook body is a Cursor payload: it names its
// event in camelCase and carries cursor_version, which no other harness sends.
func IsCursorPayload(raw []byte) bool {
	var probe struct {
		Version string `json:"cursor_version"`
		Event   string `json:"hook_event_name"`
	}
	return json.Unmarshal(raw, &probe) == nil && probe.Version != "" && probe.Event != ""
}

// Transcript is the conversation's transcript path, "" while Cursor has not
// named one.
func (p Payload) Transcript() string {
	if p.TranscriptPath == nil {
		return ""
	}
	return *p.TranscriptPath
}

// TranscriptFile is the conversation's transcript path: the one the payload names,
// else the one Cursor will write, derived from the workspace and conversation id
// (cursor-agent names none at sessionStart and the first preToolUse). A derived file
// may not exist yet; that is an empty transcript, and guardrails stay on.
func (p Payload) TranscriptFile() string {
	if t := p.Transcript(); t != "" {
		return t
	}
	return record.TranscriptPath(record.ConfigDir(), p.Folder(), p.ConversationID)
}

// Task reports a pending sub-agent launch: Cursor's Task tool. Its preToolUse is the
// only sign of a sub-agent in print mode (measured, cursor-agent 2026.10.01:
// subagentStart/subagentStop never fire, the Task call has no postToolUse, and the
// sub-agent's own events arrive under a different conversation_id with no link back).
func (p Payload) Task() (subagentType, description string, ok bool) {
	if p.HookEventName != PreToolUse || p.ToolName != "Task" {
		return "", "", false
	}
	var in struct {
		SubagentType string `json:"subagent_type"`
		Description  string `json:"description"`
	}
	if json.Unmarshal(p.ToolInput, &in) != nil {
		return "", "", false
	}
	return in.SubagentType, in.Description, true
}

// Folder is the project folder the hook belongs to.
//
// Cursor does not name a working directory on every event, and the cwd it does
// name is empty or a file:// URI, so the folder comes from workspace_roots (every
// event), then CURSOR_PROJECT_DIR (set in the hook's environment, recorded), then a
// cwd that is a plain absolute path.
func (p Payload) Folder() string {
	if len(p.WorkspaceRoots) > 0 && p.WorkspaceRoots[0] != "" {
		return p.WorkspaceRoots[0]
	}
	if d := os.Getenv("CURSOR_PROJECT_DIR"); d != "" {
		return d
	}
	if c := plainPath(p.Cwd); c != "" {
		return c
	}
	return ""
}

// plainPath turns a file:// URI into a path and keeps an absolute path; anything
// else is "".
func plainPath(s string) string {
	if strings.HasPrefix(s, "file://") {
		u, err := url.Parse(s)
		if err != nil {
			return ""
		}
		s = u.Path
	}
	if filepath.IsAbs(s) {
		return s
	}
	return ""
}

// FileWrite reports the file a pending tool call writes, and its complete new
// content. Cursor's file-writing tools reach preToolUse as tool_name "Write" with
// file_path and the whole new content, an edit included (recorded: a StrReplace in
// the transcript is a Write with the replaced content in the hook), so content is
// derivable at the pre-event. ok is false for any other tool.
func (p Payload) FileWrite() (path, content string, ok bool) {
	if p.HookEventName != PreToolUse || p.ToolName != "Write" {
		return "", "", false
	}
	var in struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
	}
	if json.Unmarshal(p.ToolInput, &in) != nil || in.FilePath == "" {
		return "", "", false
	}
	return in.FilePath, in.Content, true
}

// ShellCommand is the command line of a pending Shell call.
func (p Payload) ShellCommand() (string, bool) {
	switch p.HookEventName {
	case BeforeShellExecution:
		return p.Command, p.Command != ""
	case PreToolUse:
		if p.ToolName != "Shell" {
			return "", false
		}
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(p.ToolInput, &in) != nil || in.Command == "" {
			return "", false
		}
		return in.Command, true
	}
	return "", false
}

// SkillLoaded reports the skill a pending Read opens. Cursor has no skill tool:
// skills live in .agents/skills/<name>/SKILL.md or .cursor/skills/<name>/SKILL.md
// and the agent loads one by reading its SKILL.md, so that read is "skill loaded".
func (p Payload) SkillLoaded() (string, bool) {
	if p.HookEventName != PreToolUse || p.ToolName != "Read" {
		return "", false
	}
	var in struct {
		FilePath string `json:"file_path"`
	}
	if json.Unmarshal(p.ToolInput, &in) != nil || filepath.Base(in.FilePath) != "SKILL.md" {
		return "", false
	}
	dir := filepath.Dir(in.FilePath)
	parent := filepath.Base(filepath.Dir(dir))
	if parent != "skills" {
		return "", false
	}
	return filepath.Base(dir), true
}

// DenyOutput refuses a pending call in Cursor's shape. For preToolUse,
// beforeShellExecution, beforeReadFile and subagentStart the decision is
// {"permission":"deny", user_message, agent_message}. Both messages carry the
// reason: the recordings show only user_message reaching the run (as the rejected
// call's error), though the doc says agent_message is fed to the agent. An exit
// status of 2 blocks too (recorded), but JSON is what carries the reason cleanly.
func DenyOutput(w io.Writer, reason string) error {
	return json.NewEncoder(w).Encode(map[string]any{
		"permission":    "deny",
		"user_message":  reason,
		"agent_message": reason,
	})
}

// BlockOutput refuses to let a turn end: Cursor's stop hook continues the
// conversation with {"followup_message": ...} as the next prompt. NOTE cursor-agent
// in print mode never fires stop (recorded: runs/stop-hook-payload,
// runs/stop-block-continuation), so this takes effect in an interactive session only.
func BlockOutput(w io.Writer, reason string) error {
	return json.NewEncoder(w).Encode(map[string]any{"followup_message": reason})
}

// ContextOutput hands the agent context, for sessionStart and postToolUse
// (recorded: runs/additional-context).
func ContextOutput(w io.Writer, text string) error {
	return json.NewEncoder(w).Encode(map[string]any{"additional_context": text})
}
