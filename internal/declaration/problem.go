package declaration

import (
	"errors"
	"fmt"
	"strings"
)

// This file is the load-diagnostic vocabulary, mirroring internal/guardrail's
// problem.go for the new declaration formats.
//
// The engine's philosophy, carried over verbatim: a rule that cannot be
// understood is REFUSED by name, not silently ignored — because a rule that
// silently never fires looks exactly like a rule being satisfied, which is the
// failure this product exists to prevent. A malformed or invalid declaration
// therefore does not stop the OTHER declarations loading; it is returned as an
// Invalid carrying every fault found, so one typo cannot disarm a whole project.

// Fault says whether a problem is with what the declaration SAYS or with the
// machine it was found on — the same distinction internal/guardrail draws, and
// for the same reason: it decides which way the rule fails when we get it wrong.
//
//   - A DECLARATION fault means the rule could never fire correctly for anyone.
//     No runtime saves it, so loading it buys nothing and it is refused.
//   - An ENVIRONMENT fault means the rule knows what it wants and cannot
//     currently do it (a script that is not executable). A runtime path handles
//     that by refusing the action, so refusing to LOAD would throw away a working
//     safety mechanism to punish a missing chmod.
//
// This slice LOADS and VALIDATES declarations; it does not run their scripts, so
// it does not itself raise environment faults (a not-executable script is
// discovered by the dispatch slice, not here). The distinction is kept anyway,
// so this package's Problem is the same shape internal/guardrail's is and a
// caller reporting both can treat them uniformly, and so a later check that does
// touch the filesystem has the category ready.
type Fault int

const (
	// FaultDeclaration: the declaration is wrong. Refuse the rule.
	FaultDeclaration Fault = iota

	// FaultEnvironment: the declaration is right and the machine is not. Report
	// it and load the rule anyway.
	FaultEnvironment
)

// Problem is one thing wrong with a declaration: what kind of fault it is, where
// in the declaration it sits, and what to say about it.
//
// A Kind (a sentinel error) rather than a bare string, so a caller can react to a
// class of fault with errors.Is without matching on prose — wording is the part
// most likely to be improved, and a test keyed to it would break every time it
// was.
type Problem struct {
	// Kind is which sentinel this problem is an instance of. Compare with
	// errors.Is rather than by reading Detail.
	Kind error

	// Fault says whether this disables the rule or only warns about it.
	Fault Fault

	// Where locates the fault within the declaration for a person — "trigger 0",
	// "check 1", "require 0", or "" for a fault about the whole declaration. A
	// free-form breadcrumb rather than the old loader's fixed event/binding/hook
	// triple, because the new formats have several different nested shapes
	// (triggers, checks, prerequisites, structure entries) and one breadcrumb
	// string describes them all without a struct field per nesting.
	Where string

	// Detail is the specific complaint, without the location. Message joins the
	// two.
	Detail string
}

// The faults a declaration can have. Sentinels so a caller can ask what went
// wrong with errors.Is rather than matching strings.
var (
	// ErrMalformed: the declaration could not be read or parsed at all. There is
	// no rule to load. Declaration fault.
	ErrMalformed = errors.New("declaration: could not be read")

	// ErrBadMatch: a `match` or trigger `match` will not compile against its
	// nature's scope. The check the whole match-compilation exists for — a rule
	// whose match cannot run would silently never fire. Declaration fault.
	ErrBadMatch = errors.New("declaration: match cannot run in this scope")

	// ErrUnknownEventKind: a trigger's `on` names an event kind (or alias) the
	// nature does not admit — a Post event on a gate, a typo, Stop as a context
	// entry event. Nothing will ever wake it correctly. Declaration fault.
	ErrUnknownEventKind = errors.New("declaration: event kind not valid for this nature")

	// ErrExactlyOne: a field that must set EXACTLY one of a pair sets both or
	// neither — a Check's script/judge, a StructureEntry's glob/regex, a
	// Prerequisite's skill/context. Declaration fault.
	ErrExactlyOne = errors.New("declaration: exactly one of a pair must be set")

	// ErrAtLeastOne: a gate carries neither `require` nor `checks`, so it would
	// wake on an event and do nothing. Declaration fault.
	ErrAtLeastOne = errors.New("declaration: at least one of require/checks must be present")

	// ErrMissingField: a required field is absent or empty — a file-guard's
	// `match`, a context's `enter`/`exit`, a trigger's `event`. Declaration fault.
	ErrMissingField = errors.New("declaration: required field is missing")

	// ErrUnknownContext: a `require: [{context: X}]` names a context X that no
	// declaration defines. A configuration error caught at load — the engine
	// cannot order against a context that does not exist, and the agent cannot fix
	// it by acting. Declaration fault.
	ErrUnknownContext = errors.New("declaration: prerequisite names an unknown context")

	// ErrStrayPrepare: a check sets `prepare` without a `judge`. A prepare adds to
	// what a judge's prompt sees, so a script-only check has nothing to prepare
	// for — the field can only be a mistake. Declaration fault.
	ErrStrayPrepare = errors.New("declaration: prepare set on a check with no judge")
)

// Disabling reports whether these problems stop the rule loading. Environment
// faults alone do not.
func Disabling(problems []Problem) bool {
	for _, p := range problems {
		if p.Fault == FaultDeclaration {
			return true
		}
	}
	return false
}

// Is lets errors.Is(problem, ErrBadMatch) work.
func (p Problem) Is(target error) bool { return errors.Is(p.Kind, target) }

// Error makes a Problem usable as an error in its own right.
func (p Problem) Error() string { return p.Message() }

// Message is the human-readable line: where the fault is, then what it is.
func (p Problem) Message() string {
	if p.Where == "" {
		return p.Detail
	}
	return p.Where + ": " + p.Detail
}

// Messages renders problems for reporting, one line each.
func Messages(problems []Problem) []string {
	out := make([]string, 0, len(problems))
	for _, p := range problems {
		out = append(out, p.Message())
	}
	return out
}

// prob builds a declaration-fault problem at a location. Declaration is the
// default because every fault this slice raises is one — see Fault.
func prob(kind error, where, format string, args ...any) Problem {
	return Problem{
		Kind:   kind,
		Fault:  FaultDeclaration,
		Where:  where,
		Detail: fmt.Sprintf(format, args...),
	}
}

// oneLine flattens expr's multi-line diagnostic, which underlines the offending
// token across three lines — unreadable inside a sentence that already says which
// trigger it is about. Same helper the old loader keeps, for the same reason.
func oneLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
