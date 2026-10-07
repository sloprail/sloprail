// Package record is Cursor's session-file format: the spelling of one JSONL line
// of a conversation's agent-transcripts file and where Cursor keeps those files.
// It imports only the neutral harness package, like claudecode/record.
//
// Ground truth: the recorded transcripts in the harness-mocks repository
// (cursor-mock/snapshots/runs/*/samples/*/transcript) and the layout its mock
// reproduces (cursor-mock/internal/runner/transcript.go; capability
// session-transcript-file, provider cursor).
package record

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/sloprail/sloprail/internal/harness"
)

// Transcripts is Cursor's harness.Transcripts.
type Transcripts struct{}

// cursorLine is one line of a Cursor transcript. A conversation line is
// {"role":"user"|"assistant","message":{"content":[blocks]}}; the file closes with
// {"type":"turn_ended","status":"success"}. There is NO uuid, parent, timestamp,
// session id or cwd on any line, and no tool_result block: a tool's outcome is not
// in the transcript (true of every recorded transcript).
type cursorLine struct {
	Role    string          `json:"role"`
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
}

// ErrNotARecord: the line is JSON but neither a conversation message nor a
// turn_ended marker.
var ErrNotARecord = errors.New("cursor transcript: not a conversation record")

// ParseRecord parses one line of a Cursor transcript.
//
// Cursor writes no record identity, and ParseRecord sees one line at a time, so the
// uuid is a digest of the line itself and no record names a parent. The consequence,
// stated because the identity walk (internal/transcript) depends on it: the
// conversation's origin is the FIRST line of the file (the first parentless record),
// and its identity the digest of that line, so two conversations in one workspace
// whose first line is byte-identical (the same first prompt) share an identity. The
// Message block shapes are Cursor's own (tool_use input keys `path`/`contents`,
// tool names Shell/Write/StrReplace), left undecoded; mapping them onto a canonical
// tool vocabulary is the neutral seam's job, not this parser's.
func (Transcripts) ParseRecord(line []byte) (harness.Record, error) {
	var l cursorLine
	if err := json.Unmarshal(line, &l); err != nil {
		return harness.Record{}, err
	}
	sum := sha256.Sum256(line)
	r := harness.Record{UUID: hex.EncodeToString(sum[:16]), Message: l.Message}
	switch {
	case l.Role == "user":
		r.Type = string(harness.EntryUser)
	case l.Role == "assistant":
		r.Type = string(harness.EntryAssistant)
	case l.Type == "turn_ended":
		r.Type = string(harness.EntrySystem)
	default:
		return harness.Record{}, ErrNotARecord
	}
	return r, nil
}

// ConfigDir implements harness.Transcripts.
func (Transcripts) ConfigDir() string { return ConfigDir() }

// EncodeProjectDir implements harness.Transcripts.
func (Transcripts) EncodeProjectDir(dir string) string { return EncodeProjectDir(dir) }

// ProjectDir implements harness.Transcripts.
func (Transcripts) ProjectDir(configDir, dir string) string { return ProjectDir(configDir, dir) }
