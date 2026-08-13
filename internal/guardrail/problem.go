package guardrail

import (
	"errors"
	"fmt"
	"strings"
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
	// so nothing will ever wake it.
	ErrUnknownEventKind = errors.New("guardrail: unknown event kind")

	// ErrBadMatcher: the matcher will not compile against the fields its kind
	// carries. The fault this whole check exists for.
	ErrBadMatcher = errors.New("guardrail: matcher cannot run against this event")

	// ErrNoHooks: a binding names an event and a matcher and then does nothing.
	ErrNoHooks = errors.New("guardrail: binding has no hooks")

	// ErrBadHookType: a hook names a mechanism this engine does not have.
	ErrBadHookType = errors.New("guardrail: hook type not understood")

	// ErrBadHookCommand: a hook's command is missing, absent from disk, or not
	// something that can be executed.
	ErrBadHookCommand = errors.New("guardrail: hook command cannot be run")

	// ErrDuplicateKey: a key appears twice in the frontmatter.
	ErrDuplicateKey = errors.New("guardrail: duplicate key")

	// ErrMalformed: the declaration could not be read at all.
	ErrMalformed = errors.New("guardrail: declaration could not be read")
)

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

// at locates a problem at a binding; hook -1 means not about one hook.
func at(kind error, event string, binding, hook int, format string, args ...any) Problem {
	return Problem{
		Kind:    kind,
		Event:   event,
		Binding: binding,
		Hook:    hook,
		Detail:  fmt.Sprintf(format, args...),
	}
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
