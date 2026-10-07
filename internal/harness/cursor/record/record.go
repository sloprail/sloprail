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
	"path/filepath"
	"strings"

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

	// Timestamp is not written by Cursor: only the tool_result lines sloprail merges in
	// (opener.go) carry one, when the hook recorded the outcome.
	Timestamp string `json:"timestamp"`

	// SloprailLine is the number a merged tool_result line is cited under (opener.go);
	// Cursor's own lines have none.
	SloprailLine int `json:"sloprail_line"`

	// SloprailSidechain marks a line of a conversation not proven to be the session's own
	// root (opener.go markSidechain): its user lines are not the user's words.
	SloprailSidechain bool `json:"sloprail_sidechain"`
}

// ErrNotARecord: the line is JSON but neither a conversation message nor a
// turn_ended marker.
var ErrNotARecord = errors.New("cursor transcript: not a conversation record")

// ParseRecord parses one line of a Cursor transcript.
//
// Cursor writes no record identity, and ParseRecord sees one line at a time, so the
// uuid is a digest of the line itself (it only has to be non-empty and stable for the
// readers that skip uuid-less records) and no record names a parent. The
// conversation's identity is NOT derived from these: it is the id in the file's path
// (ConversationID), so two conversations with the same first prompt do not collide. The
// Message's tool_use blocks are put in the canonical tool vocabulary
// (harness/tools.go) by canonicalMessage, so the readers (trajectory, cite, skills)
// see Bash/Write/Edit/Read and file_path/content whatever the harness.
func (Transcripts) ParseRecord(line []byte) (harness.Record, error) {
	var l cursorLine
	if err := json.Unmarshal(line, &l); err != nil {
		return harness.Record{}, err
	}
	sum := sha256.Sum256(line)
	r := harness.Record{UUID: hex.EncodeToString(sum[:16]), Message: canonicalMessage(l.Message), Timestamp: l.Timestamp, Line: l.SloprailLine, IsSidechain: l.SloprailSidechain}
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

// ConversationID implements harness.ConversationNamer: the conversation is the name
// of the directory and of the file inside it, agent-transcripts/<id>/<id>.jsonl, the
// same id as the conversation_id of every hook payload (measured on cursor-agent
// 2026.10.01). A path not of that shape names no conversation.
func (Transcripts) ConversationID(path string) string {
	file := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	dir := filepath.Base(filepath.Dir(path))
	if file == "" || file != dir || filepath.Base(filepath.Dir(filepath.Dir(path))) != "agent-transcripts" {
		return ""
	}
	return file
}

// TranscriptPath is where Cursor writes the conversation's transcript, derived from
// the workspace and the conversation id: for the first hooks of a conversation,
// whose payload names no transcript yet (null at sessionStart and the first
// preToolUse, measured). The file may not exist: that is an empty transcript, not an
// absent record.
func TranscriptPath(configDir, workspace, conversationID string) string {
	if configDir == "" || workspace == "" || conversationID == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = r
	}
	return filepath.Join(ProjectDir(configDir, workspace), "agent-transcripts", conversationID, conversationID+".jsonl")
}

// ConfigDir implements harness.Transcripts.
func (Transcripts) ConfigDir() string { return ConfigDir() }

// EncodeProjectDir implements harness.Transcripts.
func (Transcripts) EncodeProjectDir(dir string) string { return EncodeProjectDir(dir) }

// ProjectDir implements harness.Transcripts.
func (Transcripts) ProjectDir(configDir, dir string) string { return ProjectDir(configDir, dir) }
