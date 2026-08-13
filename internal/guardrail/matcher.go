package guardrail

import (
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
)

// Matcher decides which occurrences of an event a binding responds to.
//
// Compiled once when a guardrail loads, evaluated per event. A matcher that
// does not compile, or that does not evaluate to a boolean, is refused at load
// rather than at the moment it should have fired: a rule that silently never
// fires is worse than one that will not load, because the first looks like a
// rule being satisfied.
type Matcher struct {
	src     string
	program *vm.Program

	// declared is the kind's fields, when the matcher was compiled against a
	// kind. Held so evaluation can supply a declared field the producer left
	// off the event — see env.
	declared []module.FieldDecl
}

// CompileMatcher prepares a matcher expression without knowing which kind it
// will see. An empty expression matches every occurrence, which is what a
// binding with no matcher means.
//
// This checks the expression parses and yields a boolean, and nothing about the
// names it reads — with no kind in hand there is nothing to read them against.
// Prefer CompileMatcherFor wherever the kind is known, which at load it always
// is.
func CompileMatcher(src string) (*Matcher, error) {
	return compile(src)
}

// CompileMatcherFor prepares a matcher and checks it against the fields its
// event kind actually carries.
//
// This is the check the declaration has been promising. `path startsWith "x"`
// and `pth startsWith "x"` are both well-formed expressions, and only the kind's
// declared fields distinguish the rule from the typo. Without this the second
// compiles, loads, and never fires — which reads as a rule being satisfied.
func CompileMatcherFor(src string, kind module.KindDecl) (*Matcher, error) {
	m, err := compile(src, expr.Env(matcherEnv(kind)))
	if err != nil {
		return nil, err
	}
	m.declared = kind.Fields
	return m, nil
}

func compile(src string, opts ...expr.Option) (*Matcher, error) {
	if src == "" {
		return &Matcher{}, nil
	}
	// AsBool last, so it cannot be displaced by a caller's option.
	program, err := expr.Compile(src, append(opts, expr.AsBool())...)
	if err != nil {
		return nil, fmt.Errorf("matcher %q: %w", src, err)
	}
	return &Matcher{src: src, program: program}, nil
}

// Match reports whether this matcher admits the event.
//
// The event's own fields are what the expression sees, so a rule about a file
// reads `path` and one about a command reads `invocations` — the names the
// module declared, and nothing else.
func (m *Matcher) Match(e event.Event) (bool, error) {
	if m == nil || m.program == nil {
		return true, nil
	}
	out, err := expr.Run(m.program, m.env(e))
	if err != nil {
		return false, fmt.Errorf("matcher %q: %w", m.src, err)
	}
	admitted, ok := out.(bool)
	if !ok {
		// Compilation asked for a boolean, so reaching here means the
		// expression produced something else at runtime. Refusing to guess is
		// what keeps a matcher from quietly admitting everything.
		return false, fmt.Errorf("matcher %q: produced %T, not a boolean", m.src, out)
	}
	return admitted, nil
}

// env is what the expression may read: the event's fields, and nothing beyond
// them. A matcher reaches no further than the occurrence it was handed.
//
// A field the kind DECLARES but the event omits is supplied at its type's zero
// value. That is not leniency, it is holding the producer to the declaration:
// the matcher was type-checked against the kind, so the expression has already
// been told the field is a string, and an event arriving without it makes the
// check a promise the event broke.
//
// Filling it in is what keeps that break from becoming permission. The concrete
// case: the file module omits `content` when it is empty, so a genuinely empty
// file produced a PreFileCreate with no `content` at all. `content == ""` — the
// rule for exactly that file — then evaluated a nil against a string and
// errored, and the engine's response to a matcher error is to skip the binding
// and let the write through. A rule written to catch empty files failed open on
// the only file it was about.
//
// Only DECLARED fields are supplied. An undeclared name is a typo, it is caught
// at load by CompileMatcherFor, and inventing a value for it here would undo
// that check.
//
// "Caught at load" means caught in the shipped binary, not merely by the compile
// call: Validate produces the diagnostic, LoadWith returns the declaration as
// Invalid, and BOTH hook points act on it — session start reports it, and the
// pre-tool path refuses the events the broken rule was bound to. That last link
// is load-bearing and was once missing. The pre-tool path discarded the invalid
// list, so a typo was announced once at session start and then silently disarmed
// its rule for every action after it, which made this paragraph false of the
// thing anyone actually runs. See refuseForBroken in services/sloprail, and
// tests/e2e/pre_tool/013_broken_declaration_is_not_silent, which fails if that
// link is removed again.
//
// The fill-in reaches exactly as deep as the type check does. matcherEnv and
// fieldType descend into a map's enumerated keys and a list element's declared
// fields, so an expression is type-checked to the bottom of what the module
// declared; a fill-in that stopped at the top level left a nil at every level
// below, which is the same failure the top-level fill-in was written to stop.
// The two have to agree, and the way to keep them agreeing is for both to be
// driven by the same recursion over FieldDecl.
func (m *Matcher) env(e event.Event) map[string]any {
	env := make(map[string]any, len(e.Fields)+len(m.declared))
	for k, v := range e.Fields {
		env[k] = v
	}
	for _, f := range m.declared {
		env[f.Name] = fill(f, env[f.Name])
	}
	return env
}

// fill returns what a declared field should read as, given whatever the producer
// carried for it — the carried value where there is one, completed to the shape
// the declaration promised.
//
// Absence is not the only way a field arrives without a value. A key carried as
// an explicit null is PRESENT, and is the ordinary unmarshal shape of a producer
// that sent the key with nothing in it — `{"content":null}`. Keying the fill-in
// on presence alone meant the likeliest real trigger was the one case it did not
// cover.
//
// A carried value is never replaced. Completing a structure is supplying what is
// missing from it; overwriting what arrived would make the matcher answer about
// something other than the occurrence it was handed.
func fill(f module.FieldDecl, carried any) any {
	switch f.Type {
	case module.TypeString:
		if s, ok := carried.(string); ok {
			return s
		}
		return ""

	case module.TypeBool:
		if b, ok := carried.(bool); ok {
			return b
		}
		return false

	case module.TypeList:
		items, ok := carried.([]any)
		if !ok {
			// Empty rather than nil: the expression was checked against a list,
			// and `l == nil` must read false for a field the module declared. An
			// absent list is a list of nothing, not the absence of one.
			return []any{}
		}
		if f.Elem == nil {
			// The module did not say what the list holds, so there is no shape to
			// complete its elements to — the same silence fieldType answers with
			// types.Any. Inventing one would check what was never declared.
			return items
		}
		filled := make([]any, len(items))
		for i, item := range items {
			filled[i] = fill(*f.Elem, item)
		}
		return filled

	case module.TypeMap:
		fields, _ := carried.(map[string]any)
		if len(f.Fields) == 0 {
			// Keys the module never enumerated stay open, matching the types.Any
			// fieldType gives them. Manufacturing keys here would contradict the
			// declaration rather than honour it.
			if fields == nil {
				return map[string]any{}
			}
			return fields
		}
		// A fresh map, so completing one event's value cannot mutate the event —
		// see TestMatch_DoesNotMutateTheEvent. Writing into the carried map would
		// make a matcher change the occurrence it is only supposed to ask about.
		out := make(map[string]any, len(fields)+len(f.Fields))
		for k, v := range fields {
			out[k] = v
		}
		for _, sub := range f.Fields {
			out[sub.Name] = fill(sub, out[sub.Name])
		}
		return out

	default:
		// A type this build does not recognise was checked as Any, so there is
		// no shape to honour and whatever arrived is the honest answer.
		return carried
	}
}
