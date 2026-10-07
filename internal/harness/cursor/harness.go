// Package cursor is the Cursor (cursor-agent) implementation of internal/harness:
// the hook payloads and outputs, transcript format and layout, plugin layout and
// environment that are specific to Cursor. Mirrors internal/harness/claudecode.
//
// Every shape here was read from real recordings in the harness-mocks repository
// (cursor-mock/snapshots/runs/*), whose spec/capabilities/*.yaml cursor cells also
// list where Cursor differs from Claude Code: no worktree hooks, no skill tool, no
// ask-user-question tool, no cwd on most payloads, a stop hook that never fires in
// print mode.
//
// It does NOT register itself from init: choosing the process's harness is the
// selection seam's concern (a registry with detection), and a blank import of this
// package alongside claudecode would otherwise make Register panic. Use New.
package cursor

import (
	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/harness/cursor/record"
)

// Harness is the Cursor implementation of harness.Harness.
type Harness struct{}

// New returns the Cursor harness.
func New() harness.Harness { return Harness{} }

// Name implements harness.Harness.
func (Harness) Name() string { return "cursor" }

// ResolvePlugins implements harness.Harness.
func (Harness) ResolvePlugins(projectDir, home string) (harness.Resolution, error) {
	return Resolve(projectDir, home)
}

// ProcessOfSession implements harness.Harness. Cursor keeps no session-to-process
// registry on disk that the recordings show, so no process is ever found.
func (Harness) ProcessOfSession(home, sessionID string) (harness.Process, bool) {
	return harness.Process{}, false
}

// ProcessGone implements harness.Harness: it cannot be told, never "gone".
func (Harness) ProcessGone(home string, p harness.Process) (gone, known bool) {
	return false, false
}

// SessionEnv implements harness.Harness.
func (Harness) SessionEnv(environ []string) []string { return Session(environ) }

// HermeticEnv implements harness.Harness.
func (Harness) HermeticEnv(environ []string) []string { return Hermetic(environ) }

// Transcripts implements harness.Harness.
func (Harness) Transcripts() harness.Transcripts { return record.Transcripts{} }
