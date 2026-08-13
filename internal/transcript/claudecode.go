package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// This file is the Claude Code adapter: the only place in sloprail that names
// Claude Code's own field spellings. A second harness gets a second file beside
// this one rather than a widening of Entry, so what differs between harnesses
// stays where the difference is.

// maxRecordBytes bounds one line of a session record. A single record carries a
// whole tool result, which for a file read or a long command is far past the
// scanner's default 64KiB — a smaller bound would not fail loudly, it would
// silently stop reading mid-conversation.
const maxRecordBytes = 16 * 1024 * 1024

// claudeRecord is one line of Claude Code's JSONL, in the fields we keep.
//
// ParentUUID is a pointer because the distinction that matters is null versus
// absent versus a value, and only a pointer keeps "explicitly null" — the mark
// of a conversation's origin — apart from a record that simply lacks a uuid.
type claudeRecord struct {
	Type              string          `json:"type"`
	UUID              string          `json:"uuid"`
	ParentUUID        *string         `json:"parentUuid"`
	LogicalParentUUID *string         `json:"logicalParentUuid"`
	Timestamp         string          `json:"timestamp"`
	IsSidechain       bool            `json:"isSidechain"`
	Message           json.RawMessage `json:"message"`
	ToolUseResult     json.RawMessage `json:"toolUseResult"`

	// SessionID is the id the harness wrote this record under. Kept only so
	// that a path GUESSED from a session id can be checked against what the
	// file it landed on says about itself — see BelongsToSession.
	SessionID string `json:"sessionId"`

	// Cwd is the working directory the harness ran this turn in. Kept only so
	// that a GUESSED path can be checked against the tree it was written in,
	// which is the half BelongsToSession cannot see — see BelongsToTree.
	Cwd string `json:"cwd"`
}

// entry converts a record into the canonical shape.
func (r claudeRecord) entry() Entry {
	e := Entry{
		Type:          EntryType(r.Type),
		UUID:          r.UUID,
		Timestamp:     r.Timestamp,
		IsSidechain:   r.IsSidechain,
		Message:       r.Message,
		ToolUseResult: r.ToolUseResult,
	}
	if r.ParentUUID != nil {
		e.ParentUUID = *r.ParentUUID
	}
	if r.LogicalParentUUID != nil {
		e.LogicalParentUUID = *r.LogicalParentUUID
	}
	return e
}

// Read reads a Claude Code session record into canonical entries.
//
// Lines carrying no uuid are skipped. Claude Code writes preamble and
// bookkeeping records — `custom-title`, `ai-title`, `mode`, `queue-operation`,
// `last-prompt` — that describe the session rather than anything that happened
// in it, and which carry no uuid at all; they cannot take part in a chain and
// there is nothing in them for a rule to ask about.
//
// A line that will not parse is skipped rather than fatal. The format is read
// by observation and promised by nobody, so one unrecognised line is the
// harness having changed something — a reason to keep reading the rest, not to
// refuse to answer at all. A file that cannot be opened or read IS fatal: see
// the fail-loudly reasoning on StableSessionID.
func Read(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("transcript: open %s: %w", path, err)
	}
	defer f.Close()
	return readFrom(f, path)
}

func readFrom(r io.Reader, path string) ([]Entry, error) {
	var entries []Entry
	err := scanRecords(r, path, func(rec claudeRecord) bool {
		if rec.UUID == "" {
			return true
		}
		entries = append(entries, rec.entry())
		return true
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// scanRecords walks a record file line by line, handing each parsed record to
// visit. Returning false from visit stops the walk — which is what lets the
// identity walk read a root record without reading a conversation of thousands
// behind it.
func scanRecords(r io.Reader, path string, visit func(claudeRecord) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxRecordBytes)
	for sc.Scan() {
		var rec claudeRecord
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue // see Read: an unparseable line is the format having moved
		}
		if !visit(rec) {
			return nil
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("transcript: read %s: %w", path, err)
	}
	return nil
}

// scanFile is scanRecords over a path.
func scanFile(path string, visit func(claudeRecord) bool) error {
	f, err := os.Open(path)
	if err != nil {
		// Not re-stating the path: the wrapped error already names it, and a
		// path repeated at every level of a message tells the reader nothing
		// the first mention did not.
		return fmt.Errorf("transcript: %w", err)
	}
	defer f.Close()
	return scanRecords(f, path, visit)
}
