// Package claudecode is the Claude Code implementation of internal/harness: every
// path, file schema, hook wire format and environment variable that is specific to
// Claude Code lives here and nowhere else in sloprail's production code (the
// transcript reader in internal/transcript is the one piece still to move; see the
// refactor's follow-ups). A second harness is a sibling package, not a widening of
// these types.
//
// Importing this package registers it as the process's harness (internal/harness
// Register), which is how a service's main package chooses it.
package claudecode

import "github.com/sloprail/sloprail/internal/harness"

// Harness is the Claude Code implementation of harness.Harness.
type Harness struct{}

// New returns the Claude Code harness.
func New() harness.Harness { return Harness{} }

func init() { harness.Register(New()) }

// Name implements harness.Harness.
func (Harness) Name() string { return "claudecode" }

// ResolvePlugins implements harness.Harness.
func (Harness) ResolvePlugins(projectDir, home string) (harness.Resolution, error) {
	return Resolve(projectDir, home)
}

// ProcessOfSession implements harness.Harness.
func (Harness) ProcessOfSession(home, sessionID string) (harness.Process, bool) {
	return ProcessOfSession(home, sessionID)
}

// ProcessGone implements harness.Harness.
func (Harness) ProcessGone(home string, p harness.Process) (gone, known bool) {
	return ProcessGone(home, p)
}

// SessionEnv implements harness.Harness.
func (Harness) SessionEnv(environ []string) []string { return Session(environ) }

// HermeticEnv implements harness.Harness.
func (Harness) HermeticEnv(environ []string) []string { return Hermetic(environ) }
