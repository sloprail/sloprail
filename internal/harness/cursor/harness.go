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
// Importing it registers it (wire.go); which harness a process runs is decided by
// harness.Current: SLOPRAIL_HARNESS (the plugin's hook wrapper sets it), else
// detection (Detect), else Claude Code.
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

// CommandDir implements harness.HookDir: cursor-agent runs a plugin's hooks from the
// plugin's directory, while a Shell command runs in the workspace folder the payload names.
func (Harness) CommandDir(in harness.HookInput) string { return in.Cwd }

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
