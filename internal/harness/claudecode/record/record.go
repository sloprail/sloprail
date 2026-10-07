// Package record is Claude Code's session-file format: the spelling of one JSONL
// line and where Claude Code keeps those files. It imports only the neutral
// harness package, so internal/transcript's own tests can register it.
package record

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/sloprail/sloprail/internal/harness"
)

// Transcripts is Claude Code's harness.Transcripts.
type Transcripts struct{}

// SubagentFiles implements harness.SubagentLocator: every file under
// <session>/subagents/, nested workflow directories included, records and meta files.
func (Transcripts) SubagentFiles(transcriptPath string) []harness.SubagentFile {
	if !strings.HasSuffix(transcriptPath, ".jsonl") {
		return nil
	}
	root := filepath.Join(strings.TrimSuffix(transcriptPath, ".jsonl"), "subagents")
	var out []harness.SubagentFile
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, harness.SubagentFile{Path: path, Rel: rel})
		return nil
	})
	return out
}

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
	IsMeta            bool            `json:"isMeta"`
	IsCompactSummary  bool            `json:"isCompactSummary"`
	IsTranscriptOnly  bool            `json:"isVisibleInTranscriptOnly"`
	Message           json.RawMessage `json:"message"`
	ToolUseResult     json.RawMessage `json:"toolUseResult"`
	Attachment        json.RawMessage `json:"attachment"`

	// Subtype and HookErrors are those of a system record
	// of subtype stop_hook_summary, the harness's account of one Stop hook run.
	Subtype    string   `json:"subtype"`
	HookErrors []string `json:"hookErrors"`

	// SessionID is the id the harness wrote this record under. Kept only so
	// that a path GUESSED from a session id can be checked against what the
	// file it landed on says about itself — see BelongsToSession.
	SessionID string `json:"sessionId"`

	// Cwd is the working directory the harness ran this turn in. It lets a
	// GUESSED path be checked against the tree it was written in, which is the
	// half BelongsToSession cannot see — see BelongsToTree — and it travels on
	// the Entry, where a rule resolves a tool call's relative paths against it.
	Cwd string `json:"cwd"`
}

// ParseRecord parses one line of a Claude Code session file.
func (Transcripts) ParseRecord(line []byte) (harness.Record, error) {
	var r claudeRecord
	if err := json.Unmarshal(line, &r); err != nil {
		return harness.Record{}, err
	}
	var stop *harness.StopHook
	if r.Type == "system" && r.Subtype == "stop_hook_summary" {
		stop = &harness.StopHook{
			Refused: len(r.HookErrors) > 0,
			Reasons: r.HookErrors,
		}
	}
	return harness.Record{
		StopHook:          stop,
		Type:              r.Type,
		UUID:              r.UUID,
		ParentUUID:        r.ParentUUID,
		LogicalParentUUID: r.LogicalParentUUID,
		Timestamp:         r.Timestamp,
		IsSidechain:       r.IsSidechain,
		IsMeta:            r.IsMeta,
		IsCompactSummary:  r.IsCompactSummary,
		IsTranscriptOnly:  r.IsTranscriptOnly,
		Message:           r.Message,
		ToolUseResult:     r.ToolUseResult,
		Attachment:        r.Attachment,
		SessionID:         r.SessionID,
		Cwd:               r.Cwd,
	}, nil
}

// ConfigDir implements harness.Transcripts.
func (Transcripts) ConfigDir() string { return ConfigDir() }

// EncodeProjectDir implements harness.Transcripts.
func (Transcripts) EncodeProjectDir(dir string) string { return EncodeProjectDir(dir) }

// ProjectDir implements harness.Transcripts.
func (Transcripts) ProjectDir(configDir, dir string) string { return ProjectDir(configDir, dir) }
