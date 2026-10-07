package harness

import (
	"io"
	"time"
)

// Record is one parsed line of a harness's session file: an Entry's fields plus
// what the identity and relocation walks read and Entry deliberately does not
// carry. ParentUUID is a pointer because the distinction that matters is null
// versus absent versus a value, and only a pointer keeps "explicitly null" — the
// mark of a conversation's origin — apart from a record that simply lacks a uuid.
type Record struct {
	Type              string
	UUID              string
	ParentUUID        *string
	LogicalParentUUID *string
	Timestamp         string
	IsSidechain       bool
	IsMeta            bool
	IsCompactSummary  bool
	IsTranscriptOnly  bool
	Message           []byte
	ToolUseResult     []byte
	Attachment        []byte

	// StopHook is the outcome of a Stop hook run the harness recorded, nil for a
	// record that is not one.
	StopHook *StopHook

	// SessionID is the id the harness wrote this record under. Kept only so
	// that a path GUESSED from a session id can be checked against what the
	// file it landed on says about itself.
	SessionID string

	// Cwd is the working directory the harness ran this turn in.
	Cwd string

	// Line, when non-zero, is the line number this record is cited under instead of its
	// physical place in the stream, and the record is not counted in the physical
	// numbering. A harness whose opened stream (RecordOpener) carries lines of sloprail's
	// own gives them numbers of their own, so that adding one never renumbers a line a
	// citation already names.
	Line int
}

// Entry converts a record into the canonical shape.
func (r Record) Entry() Entry {
	e := Entry{
		Type:        EntryType(r.Type),
		UUID:        r.UUID,
		Timestamp:   r.Timestamp,
		IsSidechain: r.IsSidechain,
		IsMeta:      r.IsMeta,

		IsCompactSummary:          r.IsCompactSummary,
		IsVisibleInTranscriptOnly: r.IsTranscriptOnly,
		Message:                   r.Message,
		ToolUseResult:             r.ToolUseResult,
		Attachment:                r.Attachment,
		StopHook:                  r.StopHook,
		Cwd:                       r.Cwd,
	}
	if r.ParentUUID != nil {
		e.ParentUUID = *r.ParentUUID
	}
	if r.LogicalParentUUID != nil {
		e.LogicalParentUUID = *r.LogicalParentUUID
	}
	return e
}

// ConversationNamer is implemented by a Transcripts whose session files NAME their
// conversation: Cursor writes no record identity inside the file (no uuid, no parent),
// but names the file after the conversation id every hook payload carries
// (agent-transcripts/<id>/<id>.jsonl). Where a harness does, that name is the
// conversation's identity, read from the path alone, so it needs neither the file's
// contents (the identity walk has no origin record to find) nor the file's existence
// (the first hooks of a conversation run before it is written).
type ConversationNamer interface {
	// ConversationID is the conversation the session file at path belongs to, "" when
	// the path does not name one.
	ConversationID(path string) string
}

// SubagentFile is one file of a sub-agent's record: where it is, and the path under
// which an archive of the session keeps it (relative, so a nested layout survives).
type SubagentFile struct {
	Path string
	Rel  string
}

// SubagentLocator is implemented by a Transcripts that can find, from a session's
// record alone, the files holding the sub-agents that session spawned (their records
// and companions), however the harness lays them out: Claude Code nests them under
// <session>/subagents/, Codex writes each as a rollout of its own naming its parent
// thread in its first line. A harness whose sub-agent records cannot be tied to their
// parent from what it writes does not implement it, and has none to find (and declares
// so with SubagentsUnlinkable).
type SubagentLocator interface {
	SubagentFiles(transcriptPath string) []SubagentFile
}

// SubagentsUnlinkable is implemented by a Transcripts that declares a sub-agent's
// session cannot be tied to the conversation that dispatched it: nothing a sub-agent
// writes, and nothing a hook reports, names its parent. A sub-agent's record is still
// told from a root's (the harness says which), but the link between the two does not
// exist, so what a sub-agent may cite of the user's conversation cannot be resolved and
// a root cannot enumerate its sub-agents' records.
type SubagentsUnlinkable interface {
	SubagentsUnlinkable() bool
}

// Transcripts is how a harness's session record is parsed and located: its line
// format and its on-disk layout. Everything above this (walking a chain, citing,
// the identity of a conversation) is harness-neutral and lives in internal/transcript.
type Transcripts interface {
	// ParseRecord parses one line of a session file. An error means the line is
	// not a record of this harness's format; callers decide whether that is
	// skipped or fatal.
	ParseRecord(line []byte) (Record, error)

	// ConfigDir is the harness's own configuration directory, empty when it
	// cannot be resolved.
	ConfigDir() string

	// EncodeProjectDir maps a working directory to the name the harness gives
	// that project's directory of session files.
	EncodeProjectDir(dir string) string

	// ProjectDir is where the harness keeps the session files of every session
	// run in dir (already symlink-resolved); empty when configDir is empty.
	ProjectDir(configDir, dir string) string
}

// RecordLister is implemented by a Transcripts whose project directory (ProjectDir) is not
// one flat directory of session files: Codex keeps every session in a date-sharded tree, so
// the conversation's other transcripts are found by walking it. ListRecords is every session
// file under projectDir, in no particular order.
type RecordLister interface {
	ListRecords(projectDir string) []string
}

// RecordOpener is what a Transcripts MAY implement when the record the engine should
// read is the harness's session file PLUS what sloprail itself kept beside it: Cursor's
// transcript holds no tool_result at all, so sloprail records each tool's output from
// the post-tool hook and the opener merges those back in as tool_result records.
//
// The engine reads every record through it (internal/transcript): physical line
// numbers are the opened stream's, so a citation's `<path>:<line>` resolves against the
// same stream it was made from. The stream is only ever appended to.
type RecordOpener interface {
	// OpenRecord opens the record at path as the engine reads it: JSON lines in this
	// harness's own format (ParseRecord reads each).
	OpenRecord(path string) (io.ReadCloser, error)

	// RecordVersion identifies the opened stream's current content cheaply, changing
	// whenever OpenRecord's output would (it keys the engine's parsed-record cache).
	RecordVersion(path string) (size int64, mod time.Time, err error)
}

// UserAnswerer is what a Transcripts MAY implement when the harness has a tool that
// asks the user a question and returns the answer as a tool_result (Claude Code's
// AskUserQuestion). The answer is the user's own words, so the engine cites it as
// the user's and never counts it as a tool's output. A harness without such a tool
// does not implement it.
type UserAnswerer interface {
	// QuestionTool is the name of the tool that asks the user.
	QuestionTool() string

	// IsAnswerEnvelope reports whether resultText is that tool's answer envelope,
	// whether or not any answer can be read out of it.
	IsAnswerEnvelope(resultText string) bool

	// ExtractAnswers returns only the user's answers out of that tool's result
	// text (never the questions or the harness's boilerplate); nil when the text is
	// not an answer envelope.
	ExtractAnswers(resultText string) []string
}
