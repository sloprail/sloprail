package harness

import (
	"encoding/json"
	"io"

	"github.com/sloprail/sloprail/internal/gitrepo"
)

// HookInput is what a harness's hook delivers, normalised: every harness's parser
// (HookWire.ParseHook) produces this one shape, and the engine (sr-session, the
// modules) reads nothing else. A field a harness does not report is its zero value;
// a harness that cannot report a field the engine needs says so by leaving it empty,
// never by inventing one.
//
// ToolName and ToolInput are in the canonical tool vocabulary (see tools.go): an
// adapter maps its harness's tool names and argument spellings onto it, so a module
// asking "is this a write" never learns which harness it is under.
//
// The json tags are the neutral input's own serialisation (the spelling Claude's
// hooks happen to use), which tests feed back through a harness's parser; they are
// not a contract with any harness.
//
// Resolution that needs the session record on disk (which transcript this hook
// belongs to, the state directory it is keyed by) is not a method here, because the
// transcript package imports this one: see internal/hookinput.
type HookInput struct {
	// Event is the hook event the harness raised, in its own spelling
	// ("PreToolUse", "Stop"): informational, since the engine dispatches on the
	// subcommand a hook is wired to.
	Event string `json:"hook_event_name"`

	// SessionID is the id the harness currently reports. Never the identity
	// anything is keyed on (see transcript.ResolveStableSessionID).
	SessionID string `json:"session_id"`

	// GenerationID names the agent turn (generation) the hook belongs to, for a harness
	// that reports it (Cursor); empty otherwise.
	GenerationID string `json:"generation_id,omitempty"`

	// Source is SessionStart's "startup" | "resume" | "clear" | "compact".
	Source string `json:"source"`

	// TranscriptPath is the session's record as the harness names it; empty when
	// it names none.
	TranscriptPath string `json:"transcript_path"`

	// AgentTranscriptPath is the SUB-AGENT's own record, reported only to a
	// sub-agent's hook.
	AgentTranscriptPath string `json:"agent_transcript_path"`

	// AgentID names the sub-agent within the session that dispatched it; AgentType
	// is its kind (SubagentStart/SubagentStop).
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`

	// Cwd is the working directory the hook fired in.
	Cwd string `json:"cwd"`

	// WorktreePath is the worktree a worktree-removal hook reports.
	WorktreePath string `json:"worktree_path"`

	// ToolName is the canonical name of the tool a tool hook is about.
	ToolName string `json:"tool_name"`

	// ToolUseID names the tool call, for the decision log.
	ToolUseID string `json:"tool_use_id"`

	// ToolInput is the tool's arguments, canonical-shaped and undecoded; what they
	// mean is the tool's business and each module reads only what it recognises.
	ToolInput json.RawMessage `json:"tool_input"`

	// Files are the file effects a pending tool call would have, for a harness
	// whose write tool does not state them in ToolInput's canonical shape (a patch
	// touching several files). Empty for a harness whose tools state them: the
	// modules then read ToolInput as they always did.
	Files []FileEffect `json:"files,omitempty"`

	// Result is what a tool returned, on a post-tool hook of a harness that reports it
	// (Cursor's postToolUse / postToolUseFailure); nil on every other event.
	Result *ToolResult `json:"result,omitempty"`

	// StopHookActive is true when a Stop hook fires because an earlier Stop hook
	// already kept the turn going.
	StopHookActive bool `json:"stop_hook_active"`

	// BackgroundTasks and SessionCrons are what a Stop (or SubagentStop) reports
	// still running in the background, undecoded: a list of tasks, each
	// {id, type, status, description, ...}. Absent from a harness that does not
	// report them.
	BackgroundTasks json.RawMessage `json:"background_tasks,omitempty"`
	SessionCrons    json.RawMessage `json:"session_crons,omitempty"`
}

// ToolResult is one tool call's outcome as a post-tool hook reports it. Output is the
// tool's output text; for a failed call it is the harness's error message and IsError
// is set.
type ToolResult struct {
	Output  string `json:"output"`
	IsError bool   `json:"is_error,omitempty"`
}

// FileEffectKind is what a pending tool call does to one file.
type FileEffectKind string

const (
	FileCreate FileEffectKind = "create"
	FileUpdate FileEffectKind = "update"
	FileDelete FileEffectKind = "delete"
)

// FileEffect is one file a pending tool call would change.
type FileEffect struct {
	Kind FileEffectKind

	// Path is as the harness spelled it (absolute or relative to Cwd).
	Path string

	// NewContent is the file's resulting bytes; ResultKnown says whether it could
	// be worked out, which an empty NewContent alone cannot (an underivable result
	// and a genuinely empty file must not look alike).
	NewContent  string
	ResultKnown bool
}

// IsSubagent reports whether the hook belongs to a sub-agent rather than the
// session that dispatched it. The invocation says so (these fields are reported to
// a sub-agent's hook and no other), never a transcript's contents.
func (p HookInput) IsSubagent() bool {
	return p.AgentTranscriptPath != "" || p.AgentID != ""
}

// Tool implements filemod.Pending: what the harness calls the tool it is about
// to run, in the canonical vocabulary.
func (p HookInput) Tool() string { return p.ToolName }

// Arguments implements filemod.Pending: the tool's own arguments, undecoded.
func (p HookInput) Arguments() json.RawMessage { return p.ToolInput }

// Root implements filemod.Pending: the workspace an absolute `file_path` is
// reported relative to.
//
// It is the REPOSITORY root, resolved from the cwd — not the cwd itself — because
// the observed phase resolves its root the same way (newTreeDifference calls
// gitrepo.Root), and a hook invoked below the top of the tree would otherwise make
// the two phases report different spellings of one file, so a rule binding
// PreFileCreate and PostFileCreate with a single matcher would match on one and not
// the other. An unresolvable root yields "", which filemod reads as "no workspace
// named" and leaves the path as the harness spelled it.
func (p HookInput) Root() string {
	if p.Cwd == "" {
		return ""
	}
	root, err := gitrepo.Root(p.Cwd)
	if err != nil {
		return ""
	}
	return root
}

// HeadContent implements filemod.HeadReader: a workspace-relative file's bytes in
// the repository's HEAD commit, when they are at most limit bytes (git is asked the
// size first, so an oversize blob is never read). The file module uses it for the
// markers of a delete whose bytes it could not read on disk.
func (p HookInput) HeadContent(path string, limit int64) (string, bool) {
	root := p.Root()
	if root == "" || path == "" {
		return "", false
	}
	return gitrepo.ContentAtWithin(root, "HEAD", "./"+path, limit)
}

// Decision is what the engine decides about a hook.
type Decision int

const (
	// Allow lets the action or the end of the cycle proceed. Rendered as no output
	// unless the response carries context or a message.
	Allow Decision = iota
	// Deny refuses a pending tool call; Reason is told to the agent.
	Deny
	// Block refuses to let a cycle end (Stop, SubagentStop); Reason is told to the
	// agent, which continues.
	Block
)

// HookResponse is what the engine answers a hook with, before a harness spells it.
type HookResponse struct {
	// Event is the hook event being answered, as HookInput.Event reported it; some
	// harnesses name it in the answer.
	Event string

	Decision Decision

	// Reason accompanies Deny and Block.
	Reason string

	// AdditionalContext is text added to the agent's context.
	AdditionalContext string

	// SystemMessage is a note shown to the person, not the agent.
	SystemMessage string
}

// HookWire is the half of a Harness that speaks its hook protocol.
type HookWire interface {
	// ParseHook reads a hook's standard input. An unreadable or empty body yields
	// a zero input rather than an error: a hook that cannot read its input has
	// nothing to judge, and an engine that failed here would block work for a
	// reason that has nothing to do with any rule the project declared.
	ParseHook(r io.Reader) HookInput

	// RenderHook writes the response in the harness's own output format.
	RenderHook(w io.Writer, resp HookResponse) error
}
