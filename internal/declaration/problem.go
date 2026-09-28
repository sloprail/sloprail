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

	// ErrAtLeastOne: a gate or file-guard carries neither `require` nor `checks`,
	// so it would select its subject — a gate waking on an event, a file-guard
	// matching a file — and have nothing to say about it. Declaration fault.
	ErrAtLeastOne = errors.New("declaration: at least one of require/checks must be present")

	// ErrMissingField: a required field is absent or empty — a file-guard's
	// `match`, a context's `enter`/`exit`, a trigger's `event`. Declaration fault.
	ErrMissingField = errors.New("declaration: required field is missing")

	// ErrUnknownContext: a `require: [{context: X}]` names a context X that no
	// declaration defines. A configuration error caught at load — the engine
	// cannot order against a context that does not exist, and the agent cannot fix
	// it by acting. Declaration fault.
	ErrUnknownContext = errors.New("declaration: prerequisite names an unknown context")

	// ErrBadFilesEntry: a `require: [{skill, files}]` prerequisite's `files` is
	// set without `skill` (a subpage of nothing), or one of its entries is not a
	// plain relative path inside the skill's own directory (empty, absolute, or
	// climbing out with `..`). Declaration fault — caught at load, not at
	// runtime, since the shape is checkable without reading the trajectory.
	ErrBadFilesEntry = errors.New("declaration: prerequisite's files entry is invalid")

	// ErrStrayPrepare: a check sets `prepare` without a `judge`. A prepare adds to
	// what a judge's prompt sees, so a script-only check has nothing to prepare
	// for — the field can only be a mistake. Declaration fault.
	ErrStrayPrepare = errors.New("declaration: prepare set on a check with no judge")

	// ErrStrayModel: a check sets `model` or `timeout` without a `judge`. Both
	// tune a model call, so a script-only check — which makes no model call and
	// bounds its own runtime — has nothing to apply them to. The field can only
	// be a mistake, refused rather than ignored so the author learns it does
	// nothing. Declaration fault. (One sentinel for both, the same way
	// ErrStrayPrepare covers the single stray-on-script case.)
	ErrStrayModel = errors.New("declaration: model/timeout set on a check with no judge")

	// ErrBadModel: a check's `model` is not a well-formed modelset — an empty
	// set, or an entry that is empty (a stray or trailing comma). Mirrors what
	// sr-agent's own --model parsing refuses, so a set that would fail at the
	// judge is caught at load instead. Declaration fault.
	ErrBadModel = errors.New("declaration: judge model is not a well-formed modelset")

	// ErrBadTimeout: a check's `timeout` does not parse as a Go duration string,
	// or is <= 0 — a timeout that never fires is not a timeout. Declaration
	// fault.
	ErrBadTimeout = errors.New("declaration: judge timeout is not a positive duration")

	// ErrStrayAllowedTools: a check sets `allowed_tools` without a `judge`. It
	// grants tools to a judge's agent, so a script-only check — which names its own
	// tools by being an executable — has nothing to apply them to. Its own sentinel
	// rather than folded into ErrStrayModel because the fix names a different field.
	// Declaration fault.
	ErrStrayAllowedTools = errors.New("declaration: allowed_tools set on a check with no judge")

	// ErrBadAllowedTools: a check's `allowed_tools` carries an entry that is not
	// one tool rule — empty (a blank name grants nothing), two rules run together
	// in one item, or a scoped rule whose parentheses do not balance. Mirrors
	// ErrBadModel's empty-entry refusal. Declaration fault.
	ErrBadAllowedTools = errors.New("declaration: judge allowed_tools has a malformed entry")

	// ErrStrayDisallowedTools: a check sets `disallowed_tools` without a `judge`.
	// It denies tools to a judge's agent, and a script check has none. Declaration
	// fault.
	ErrStrayDisallowedTools = errors.New("declaration: disallowed_tools set on a check with no judge")

	// ErrBadDisallowedTools: a check's `disallowed_tools` carries an entry that is
	// not one tool rule, for the same reasons as ErrBadAllowedTools. A malformed
	// deny is worse than a malformed allow: it would silently deny nothing.
	// Declaration fault.
	ErrBadDisallowedTools = errors.New("declaration: judge disallowed_tools has a malformed entry")

	// ErrBadValue: a field that takes one of a fixed set of values names
	// something outside it — a file-guard's `deletions:` other than skip /
	// include / only. Refused rather than read as the default, because a typo
	// that silently became `skip` would switch off exactly the deletions the
	// author wrote the key to catch. Declaration fault.
	ErrBadValue = errors.New("declaration: field value is not one of its allowed values")

	// ErrBadScope: a structure gate's `scope` is wrong for where it was found or
	// for what a scope is — a project's own structure declaring one (the
	// project's covers the whole tree), a scope entry that is a regex (glob-only
	// for now), a glob that does not name a folder (no trailing `/`), or one that
	// covers the whole tree (`**/`, `*/`, `/`). Declaration fault.
	ErrBadScope = errors.New("declaration: structure scope is not a folder a plugin may own")

	// ErrOutsideScope: a plugin's structure gate allows or denies a path outside
	// the literal folders its `scope` names. The entry could never decide
	// anything (a plugin's structure is inert outside its scope), so it can only
	// be a mistake — most likely a scope and an entry that disagree about where
	// the plugin's files live. Declaration fault.
	ErrOutsideScope = errors.New("declaration: structure entry lies outside the plugin's scope")
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
