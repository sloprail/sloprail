package harness

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// Harness is everything sloprail's harness-neutral code asks of the coding agent
// it runs inside. One implementation per harness lives in a sibling package,
// internal/harness/<name>/, and registers itself from init (Register); a service's
// main package chooses the harness by importing that package (blank import), and
// no other package may import an implementation (tests/repo enforces it). A main
// may import several; which one a process runs is decided by Current. A main
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

	// Transcripts is how the harness's session record is parsed and located.
	Transcripts() Transcripts

	// The hook protocol: parsing a hook's input and spelling the engine's answer.
	HookWire
}

// Detector is what a Harness MAY also implement so that it can be recognised from
// the environment of the process running under it (see Current).
type Detector interface {
	// Detect reports whether environ (KEY=VALUE entries) is that of a process
	// running under this harness, from variables the harness itself sets.
	Detect(environ []string) bool
}

// TranscriptLocator is what a Harness MAY implement when a hook's payload does not
// always name the session's record (Cursor's first events carry a null path): it
// finds the record another way, so the engine does not conclude "no transcript"
// and switch guardrails off for a record that exists. It is asked only when the
// payload names none, and returns "" when it cannot tell.
type TranscriptLocator interface {
	LocateTranscript(in HookInput) string
}

// ChildEnvBlocklist is what a Harness MAY implement to name the variables that
// carry its own live session's identity or IPC (beyond the ones sr-agent already
// strips), so a one-shot judge started from within a session does not inherit them.
type ChildEnvBlocklist interface {
	ChildEnvBlocklist() []string
}

// CurrentSessionLocator is what a Harness MAY implement to find the record of the
// session a command is running inside, from that command's own environment (not from a
// hook payload): the agent, or a rule's script, running `sr-session` from a tool shell.
// getenv is the process's environment; "" means no session is named or its record is
// not there. A harness without it is read through Transcripts alone (Claude Code's
// exported session id and projects layout, internal/transcript).
type CurrentSessionLocator interface {
	CurrentSessionPath(cwd string, getenv func(string) string) string
}

// SkillDirs is what a Harness MAY implement to name where a project keeps its own
// skills, relative to the project root. A harness that does not is taken to use
// DefaultSkillDir.
type SkillDirs interface {
	ProjectSkillDirs() []string
}

// DefaultSkillDir is where a project's own skills live for a harness that names none
// (Claude Code's layout).
const DefaultSkillDir = ".claude/skills"

// ProjectSkillDirs is h's project-relative skill directories.
func ProjectSkillDirs(h Harness) []string {
	if s, ok := h.(SkillDirs); ok {
		if dirs := s.ProjectSkillDirs(); len(dirs) > 0 {
			return dirs
		}
	}
	return []string{DefaultSkillDir}
}

// JudgeGate is what a Harness MAY implement when it cannot confine a launched judge
// by its own permissions alone (Cursor's cannot say "writable only here"): the engine's
// pre-tool hook, which fires inside the judge too, asks it whether the pending call
// is outside what the judge was granted, getenv being the hook's environment (where
// sr-agent put the grant). "" means allowed, or not a launched judge.
type JudgeGate interface {
	JudgeRefusal(in HookInput, getenv func(string) string) string
}

// Default is the harness a process runs under when nothing selects another.
const Default = "claudecode"

// SelectEnv names, when set, the harness a process runs under, overriding
// detection. It is how a harness's plugin wires its hooks explicitly (the hook
// command sets it), where the environment alone would not say.
const SelectEnv = "SLOPRAIL_HARNESS"

var (
	mu       sync.RWMutex
	registry = map[string]Harness{}
)

// Register adds h to the process's harnesses, keyed by Name; registering a name
// again replaces it. An implementation package calls it from init, so importing the
// package is what makes it available; a service's main package chooses which to
// import and Current chooses among those. Adding a harness is a new file that
// registers itself, never an edit to a shared switch.
func Register(h Harness) {
	mu.Lock()
	defer mu.Unlock()
	registry[h.Name()] = h
}

// Current is the harness this process runs under, decided in this order:
//
//  1. SelectEnv, when set: it must name a registered harness (anything else
//     panics, since a misspelt name silently falling back would run the wrong
//     harness's rules).
//  2. The registered harnesses that implement Detector and recognise the
//     environment, in name order; the first wins.
//  3. Default, when registered; else the only registered harness.
//
// It panics when none is registered: a service main that forgot to import its
// harness would otherwise run with plugin discovery and session handling silently
// doing nothing, which is the failure this product exists to prevent.
func Current() Harness { return Select(os.Environ()) }

// Select is Current over an explicit environment.
func Select(environ []string) Harness {
	mu.RLock()
	defer mu.RUnlock()
	if len(registry) == 0 {
		panic("harness: none registered; import an internal/harness/<name> package from the service's main package")
	}
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, SelectEnv+"="); ok && v != "" {
			h, found := registry[v]
			if !found {
				panic(fmt.Sprintf("harness: %s=%q names no registered harness (registered: %s)", SelectEnv, v, names()))
			}
			return h
		}
	}
	for _, name := range sortedNames() {
		if d, ok := registry[name].(Detector); ok && d.Detect(environ) {
			return registry[name]
		}
	}
	if h, ok := registry[Default]; ok {
		return h
	}
	for _, h := range registry {
		return h
	}
	panic("unreachable")
}

func sortedNames() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func names() string { return strings.Join(sortedNames(), ", ") }
