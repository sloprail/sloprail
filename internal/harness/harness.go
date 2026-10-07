package harness

import (
	"fmt"
	"sync"
)

// Harness is everything sloprail's harness-neutral code asks of the coding agent
// it runs inside. One implementation per harness lives in a sibling package,
// internal/harness/<name>/, and registers itself from init (Register); a service's
// main package chooses the harness by importing that package (blank import), and
// no other package may import an implementation (tests/repo enforces it).
//
// The interface is deliberately small: what generic code (the engine, gates,
// file-guards, sr-checks) needs to be harness-independent, and nothing a single
// service alone uses. A service main that talks to one harness directly (sr-agent
// launching `claude`, sr-session reading Claude hook payloads) imports the
// implementation package itself; that is the wiring, not a leak.
type Harness interface {
	// Name is the harness's short name ("claudecode").
	Name() string

	// ResolvePlugins reads a project's enabled plugins from the harness's own
	// configuration and locates each one. See Resolution and Unresolved.
	ResolvePlugins(projectDir, home string) (Resolution, error)

	// ProcessOfSession finds the live harness process running a session id, or
	// false when none is recorded (or the harness's session directory cannot be read).
	ProcessOfSession(home, sessionID string) (Process, bool)

	// ProcessGone reports whether a recorded process has ended. known is false when
	// that cannot be told, which callers must never read as "gone".
	ProcessGone(home string, p Process) (gone, known bool)

	// SessionEnv returns environ without the enclosing harness session's identity
	// variables, so a subprocess a launcher starts does not inherit the outer session.
	SessionEnv(environ []string) []string

	// HermeticEnv is SessionEnv plus everything else that would point a launched
	// process at operator or outer-run state.
	HermeticEnv(environ []string) []string
}

var (
	mu      sync.RWMutex
	current Harness
)

// Register makes h the process's harness. An implementation package calls it from
// init, so importing the package is what selects it; a second, different harness
// registering in the same process is a wiring mistake and panics rather than
// letting import order pick one.
func Register(h Harness) {
	mu.Lock()
	defer mu.Unlock()
	if current != nil && current.Name() != h.Name() {
		panic(fmt.Sprintf("harness: %q registered while %q already is; a process runs one harness", h.Name(), current.Name()))
	}
	current = h
}

// Current is the registered harness. It panics when none is: a service main that
// forgot to import its harness would otherwise run with plugin discovery and
// session handling silently doing nothing, which is the failure this product
// exists to prevent.
func Current() Harness {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		panic("harness: none registered; import an internal/harness/<name> package from the service's main package")
	}
	return current
}
