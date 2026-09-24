// Package transcript reads a harness's record of a session into the one shape
// every rule is written against.
//
// A harness records a great deal more than a rule ever asks about — routing,
// versions, request ids, its own bookkeeping. Carrying that through would make
// this shape a mirror of Claude Code's, which is the opposite of the point:
// every field kept here is a field each new harness must be normalised into.
//
// The shape is taken from what Claude Code already writes, because a format
// that has carried real sessions is a better starting point than one reasoned
// out cold. It is ours from then on, and Claude Code is simply the harness
// needing no translation today.
package transcript

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

	// Message is what was said or done — the message content for a turn, the
	// invocation for a tool call. Left undecoded: what is inside is the
	// harness's shape, and a rule that wants to reach into it is better served
	// by the tools it already uses for JSON than by a struct this package would
	// have to widen for every harness.
	Message json.RawMessage `json:"message,omitempty"`

	// ToolUseResult is what a tool returned. This is where evidence of what an
	// action actually produced lives, as against what was claimed of it.
	ToolUseResult json.RawMessage `json:"toolUseResult,omitempty"`
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
)
