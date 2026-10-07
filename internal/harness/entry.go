package harness

import "encoding/json"

// Entry is one thing that happened during a session.
type Entry struct {
	// Type is what kind of entry this is.
	Type EntryType `json:"type"`

	// UUID identifies this entry within the session.
	UUID string `json:"uuid"`

	// ParentUUID is the entry this one followed, empty on the first. A session
	// is a chain rather than a list, and that chain is what makes an origin
	// findable.
	ParentUUID string `json:"parentUuid,omitempty"`

	// LogicalParentUUID is where this record continues from, when it opens a
	// file that continues an earlier conversation. Present on the first record
	// of a transcript written after a restart or a compaction, and pointing
	// into whichever older file holds the record it resumes.
	//
	// It exists because such a record also has no parent — a new file has
	// nothing before it to point at — so ParentUUID alone would make every
	// restart look like a fresh conversation.
	LogicalParentUUID string `json:"logicalParentUuid,omitempty"`

	// Timestamp is when it happened.
	Timestamp string `json:"timestamp,omitempty"`

	// IsSidechain reports whether this entry belongs to a sub-agent rather than
	// the main line of work. A rule asking what the agent did usually means the
	// main line; one asking whether work was delegated means precisely this.
	IsSidechain bool `json:"isSidechain"`

	// IsMeta reports a user-typed entry the HARNESS wrote rather than the person:
	// Claude Code records a Stop hook's refusal ("Stop hook feedback: …"), a
	// skill's already-loaded notice and similar as `type: "user"` with string
	// content, indistinguishable by shape from something the person typed. A rule
	// counting the person's turns needs to leave these out; one finding where the
	// agent's latest reply begins needs to keep them. Omitted when false.
	IsMeta bool `json:"isMeta,omitempty"`

	// IsCompactSummary reports the record Claude Code writes when it compacts a
	// conversation (/compact, or automatically near the context limit): a
	// `type: "user"` entry with no isMeta whose text is a MODEL's summary of
	// what came before. Never the person's words. Omitted when false.
	IsCompactSummary bool `json:"isCompactSummary,omitempty"`

	// IsVisibleInTranscriptOnly reports a record the harness shows in the
	// transcript view but never sent the agent as the person's turn — the
	// compaction summary carries it, beside IsCompactSummary. Omitted when
	// false.
	IsVisibleInTranscriptOnly bool `json:"isVisibleInTranscriptOnly,omitempty"`

	// Message is what was said or done — the message content for a turn, the
	// invocation for a tool call. Left undecoded: what is inside is the
	// harness's shape, and a rule that wants to reach into it is better served
	// by the tools it already uses for JSON than by a struct this package would
	// have to widen for every harness.
	Message json.RawMessage `json:"message,omitempty"`

	// ToolUseResult is what a tool returned. This is where evidence of what an
	// action actually produced lives, as against what was claimed of it.
	ToolUseResult json.RawMessage `json:"toolUseResult,omitempty"`

	// Cwd is the working directory the harness ran this record in — where a
	// tool call's relative path, and a shell command line's own starting
	// directory, resolve from. Claude Code writes it on every conversational
	// record and moves it when the agent's shell `cd`s persistently. Empty when
	// the harness did not say.
	Cwd string `json:"cwd,omitempty"`

	// Attachment is the payload of an EntryAttachment record — Claude Code writes
	// it in a top-level `attachment` field, a sibling of `message` rather than a
	// shape inside it, so it needs its own field: an attachment entry's Message
	// is empty. Left undecoded for the same reason Message is: what is inside is
	// the harness's own shape, per `attachment.type`.
	Attachment json.RawMessage `json:"attachment,omitempty"`

	// StopHook is how a Stop hook run ended, on the EntrySystem record the harness
	// writes for it (Claude Code's `stop_hook_summary`). Nil on every other entry,
	// and on harnesses whose records carry no such outcome.
	StopHook *StopHook `json:"stopHook,omitempty"`
}

// StopHook is the outcome of one Stop hook run: whether the hooks let the turn end,
// and why not when they did not.
type StopHook struct {
	// Refused is true when the hooks sent the agent back to work: a hook listed an error.
	Refused bool `json:"refused"`

	// Reasons are the hook errors the harness listed, empty when Refused is false.
	Reasons []string `json:"reasons,omitempty"`
}

// EntryType is what kind of entry an entry is.
//
// A string rather than a closed set of constants: the three below are what a
// rule is written against, but a harness writes kinds beyond them (Claude Code
// emits `attachment`, among others) and inventing a fourth name for "something
// else" would lose which one it actually was. An unrecognised kind travels
// through under its own name, and a rule naming it is a rule that works.
type EntryType string

const (
	// EntryUser is something the person said.
	EntryUser EntryType = "user"

	// EntryAssistant is something the agent said or did, including its tool
	// calls.
	EntryAssistant EntryType = "assistant"

	// EntrySystem is a record the harness wrote about the session itself rather
	// than about the work.
	EntrySystem EntryType = "system"

	// EntryAttachment is something the harness attached to the conversation
	// outside the ordinary user/assistant exchange — Claude Code writes a
	// person's mid-turn message this way (a `queued_command` attachment) as well
	// as bookkeeping the person never typed (an `environment` snapshot, a
	// `model` identity notice, a completed background task's notification). Which
	// of those a given attachment is lives in its own `attachment.type` field,
	// inside Message — see cite.go's queued-command handling for the one kind a
	// rule grounding a claim in the user's own words must still recognise.
	EntryAttachment EntryType = "attachment"
)
