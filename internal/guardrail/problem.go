package guardrail

import (
	"errors"
	"fmt"
	"strings"
)

// Fault says whether a problem is with what the declaration SAYS or with the
// machine it was found on. It decides whether the rule still loads.
//
// The distinction is not tidiness. It is which way the rule fails when we get
// it wrong:
//
//   - A declaration fault means the rule could never fire correctly for anyone.
//     There is no runtime that saves it, so loading it buys nothing and it is
//     refused.
//
//   - An environment fault means the rule knows exactly what it wants to do and
//     cannot currently do it. A runtime path already handles that correctly, by
//     refusing the action — so refusing to LOAD would throw away a working
//     safety mechanism to punish a missing chmod, and convert a rule that
//     would have blocked into one that permits.
//
// The second is the fail-open bug by another route: a project with one
// un-chmod'ed hook would be silently unguarded at every tool call, with the
// only explanation printed at a session start nobody was watching.
type Fault int

const (
	// FaultDeclaration: the file is wrong. Refuse the rule.
	FaultDeclaration Fault = iota

	// FaultEnvironment: the file is right and the machine is not. Report it
	// loudly and load the rule anyway, so the runtime refuses when the hook
	// cannot run. `chmod +x` fixes it without touching the declaration.
	FaultEnvironment
)

// Problem is one thing wrong with a declaration: what kind of fault it is,
// where in the declaration it sits, and what to say about it.
//
// A kind rather than a string, so a caller can react to a class of fault
// without matching on prose. Wording is the part most likely to be improved,
// and a test or a UI keyed to it would break every time it was.
type Problem struct {
	// Kind is which sentinel this problem is an instance of. Compare with
	// errors.Is rather than by reading Message.
	Kind error

	// Fault says whether this disables the rule or only warns about it.
	Fault Fault

	// Event and Binding locate the fault; Hook is -1 when the problem is not
	// about a particular hook. Empty Event means the whole declaration.
	Event   string
	Binding int
	Hook    int

	// Detail is the specific complaint, without the location — "type \"script\"
	// not understood". Message joins the two.
	Detail string
}

// The faults a declaration can have. Sentinels so a caller can ask what went
// wrong without matching strings, per the package's error convention.
var (
	// ErrUnknownEventKind: the declaration binds an event no module produces,
	// so nothing will ever wake it. Declaration fault.
	ErrUnknownEventKind = errors.New("guardrail: unknown event kind")

	// ErrBadMatcher: the matcher will not compile against the fields its kind
	// carries. The fault this whole check exists for. Declaration fault.
	ErrBadMatcher = errors.New("guardrail: matcher cannot run against this event")

	// ErrNoHooks: a binding names an event and a matcher and then does nothing.
	// Declaration fault.
	ErrNoHooks = errors.New("guardrail: binding has no hooks")

	// ErrBadHookType: a hook names a mechanism this engine does not have.
	// Declaration fault.
	ErrBadHookType = errors.New("guardrail: hook type not understood")

	// ErrNoHookCommand: a hook declares no command at all. Declaration fault —
	// nothing on disk can make an empty command runnable.
	ErrNoHookCommand = errors.New("guardrail: hook declares no command")

	// ErrHookNotRunnable: the command is named but cannot be executed — absent,
	// a directory, or not marked executable. ENVIRONMENT fault: the declaration
	// is right and the machine is not, so the rule still loads and the runtime
	// refuses on its behalf.
	ErrHookNotRunnable = errors.New("guardrail: hook command cannot be run")

	// ErrDuplicateKey: a key appears twice in the frontmatter. Declaration
	// fault.
	ErrDuplicateKey = errors.New("guardrail: duplicate key")

	// ErrMalformed: the declaration could not be read at all. Declaration
	// fault — there is no rule to load.
	ErrMalformed = errors.New("guardrail: declaration could not be read")
)

// Disabling reports whether these problems stop the rule loading. Environment
// faults alone do not: the rule is loaded so its hooks can refuse.
func Disabling(problems []Problem) bool {
	for _, p := range problems {
		if p.Fault == FaultDeclaration {
			return true
		}
	}
	return false
}

// Partition splits problems into those that disable the rule and those that
// only warn about it, so a caller can report each in its own voice.
func Partition(problems []Problem) (disabling, warnings []Problem) {
	for _, p := range problems {
		if p.Fault == FaultDeclaration {
			disabling = append(disabling, p)
			continue
		}
		warnings = append(warnings, p)
	}
	return disabling, warnings
}

// Is lets errors.Is(problem, ErrBadMatcher) work.
func (p Problem) Is(target error) bool { return errors.Is(p.Kind, target) }

// Error makes a Problem usable as an error in its own right.
func (p Problem) Error() string { return p.Message() }

// Message is the human-readable line: where the fault is, then what it is.
func (p Problem) Message() string {
	if p.Event == "" {
		return p.Detail
	}
	at := fmt.Sprintf("event %q", p.Event)
	if p.Binding >= 0 {
		at += fmt.Sprintf(" binding %d", p.Binding)
	}
	if p.Hook >= 0 {
		at += fmt.Sprintf(" hook %d", p.Hook)
	}
	return at + ": " + p.Detail
}

// at locates a declaration fault at a binding; hook -1 means not about one
// hook. Declaration is the default because most faults are: an environment one
// is the exception and says so at its call site, via atEnv.
func at(kind error, event string, binding, hook int, format string, args ...any) Problem {
	return Problem{
		Kind:    kind,
		Fault:   FaultDeclaration,
		Event:   event,
		Binding: binding,
		Hook:    hook,
		Detail:  fmt.Sprintf(format, args...),
	}
}

// atEnv locates an environment fault — one that warns without disabling.
func atEnv(kind error, event string, binding, hook int, format string, args ...any) Problem {
	p := at(kind, event, binding, hook, format, args...)
	p.Fault = FaultEnvironment
	return p
}

// Messages renders problems for reporting, one line each.
func Messages(problems []Problem) []string {
	out := make([]string, 0, len(problems))
	for _, p := range problems {
		out = append(out, p.Message())
	}
	return out
}

// oneLine flattens expr's multi-line diagnostic, which underlines the offending
// token across three lines. Useful in a terminal, unreadable inside a sentence
// that already says which binding it is about.
func oneLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
