package harness

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

	// SessionID is the id the harness wrote this record under. Kept only so
	// that a path GUESSED from a session id can be checked against what the
	// file it landed on says about itself.
	SessionID string

	// Cwd is the working directory the harness ran this turn in.
	Cwd string
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
