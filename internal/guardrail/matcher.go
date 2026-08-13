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
	env, err := m.env(e)
	if err != nil {
		return false, fmt.Errorf("matcher %q: %w", m.src, err)
	}
	out, err := expr.Run(m.program, env)
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
//
// A field carried at the WRONG type errors rather than filling in. See fill:
// absence and disagreement are different facts, and only the first has a right
// answer.
func (m *Matcher) env(e event.Event) (map[string]any, error) {
	env := make(map[string]any, len(e.Fields)+len(m.declared))
	for k, v := range e.Fields {
		env[k] = v
	}
	for _, f := range m.declared {
		v, err := fill(f, env[f.Name])
		if err != nil {
			return nil, err
		}
		env[f.Name] = v
	}
	return env, nil
}

// fill returns what a declared field should read as, given whatever the producer
// carried for it — the carried value where there is one, completed to the shape
// the declaration promised — or an error when what arrived contradicts the
// declaration outright.
//
// # Absent, and wrong, are different facts
//
// ABSENT has a right answer. A field the producer left off, or sent as an
// explicit null, is a field with no value, and the declaration already says what
// no-value looks like for it: "" for a string, false for a bool, an empty list
// or map. Supplying that is holding the producer to the declaration the matcher
// was type-checked against. A key carried as an explicit null is PRESENT, and is
// the ordinary unmarshal shape of a producer that sent the key with nothing in
// it — `{"content":null}` — so keying on presence alone missed the likeliest
// real trigger. Both are absence, and both fill in.
//
// WRONG has no right answer, and this is where an earlier version of this
// function reintroduced the very fail-open it was written to close. It keyed on
// the VALUE rather than on presence: any carried value that was not already the
// declared type was replaced by the zero value. So for `path startsWith
// "guarded/"`, a producer carrying `path` as 42, or true, or a list, yielded ""
// — and the rule returned `admitted=false, err=nil`. The write proceeded, and
// the guardrail reported nothing, because a clean false is indistinguishable
// from a rule that legitimately did not match. That is strictly worse than the
// presence-keyed version it replaced, which at least errored and was caught by
// the engine's refusal path.
//
// A value of the wrong type is not a missing value. It is the engine being
// unable to ANSWER whether the rule applies: `42 startsWith "guarded/"` has no
// truth value, and inventing one — in either direction — is the engine deciding
// enforcement on its own account. So it errors, and joins the same family as a
// matcher that cannot be evaluated: the caller in services/sloprail refuses the
// action and says why. See TestMatch_WrongTypedCarriedValueErrors.
//
// A carried value of the RIGHT type is never replaced. Completing a structure is
// supplying what is missing from it; overwriting what arrived would make the
// matcher answer about something other than the occurrence it was handed.
func fill(f module.FieldDecl, carried any) (any, error) {
	// Absence, in both its spellings. Checked before the type switch so every
	// branch below is about a value that actually arrived.
	if carried == nil {
		return zero(f), nil
	}

	switch f.Type {
	case module.TypeString:
		s, ok := carried.(string)
		if !ok {
			return nil, wrongType(f, "string", carried)
		}
		return s, nil

	case module.TypeBool:
		b, ok := carried.(bool)
		if !ok {
			return nil, wrongType(f, "bool", carried)
		}
		return b, nil

	case module.TypeList:
		items, ok := carried.([]any)
		if !ok {
			return nil, wrongType(f, "list", carried)
		}
		if f.Elem == nil {
			// The module did not say what the list holds, so there is no shape to
			// complete its elements to — the same silence fieldType answers with
			// types.Any. Inventing one would check what was never declared.
			return items, nil
		}
		filled := make([]any, len(items))
		for i, item := range items {
			v, err := fill(*f.Elem, item)
			if err != nil {
				return nil, fmt.Errorf("in %s[%d]: %w", name(f), i, err)
			}
			filled[i] = v
		}
		return filled, nil

	case module.TypeMap:
		fields, ok := carried.(map[string]any)
		if !ok {
			return nil, wrongType(f, "map", carried)
		}
		if len(f.Fields) == 0 {
			// Keys the module never enumerated stay open, matching the types.Any
			// fieldType gives them. Manufacturing keys here would contradict the
			// declaration rather than honour it.
			return fields, nil
		}
		// A fresh map, so completing one event's value cannot mutate the event —
		// see TestMatch_DoesNotMutateTheEvent. Writing into the carried map would
		// make a matcher change the occurrence it is only supposed to ask about.
		out := make(map[string]any, len(fields)+len(f.Fields))
		for k, v := range fields {
			out[k] = v
		}
		for _, sub := range f.Fields {
			v, err := fill(sub, out[sub.Name])
			if err != nil {
				return nil, fmt.Errorf("in %s: %w", name(f), err)
			}
			out[sub.Name] = v
		}
		return out, nil

	default:
		// A type this build does not recognise was checked as Any, so there is
		// no shape to honour and whatever arrived is the honest answer.
		return carried, nil
	}
}

// zero is what a declared field reads as when the producer carried no value for
// it. The shape the expression was type-checked against, and nothing more.
//
// It recurses for the same reason fill does, and has to reach exactly as deep:
// an omitted map whose keys the module ENUMERATED was type-checked as a closed
// structure over those keys, so handing back a bare empty map leaves a nil at
// `meta.user` where the expression was promised a string — the same failure one
// level down that the top-level fill-in exists to stop.
//
// It cannot fail. Every value it produces it invents from the declaration, so
// there is nothing here that can contradict one.
func zero(f module.FieldDecl) any {
	switch f.Type {
	case module.TypeString:
		return ""
	case module.TypeBool:
		return false
	case module.TypeList:
		// Empty rather than nil: the expression was checked against a list, and
		// `l == nil` must read false for a field the module declared. An absent
		// list is a list of nothing, not the absence of one. Its element shape,
		// declared or not, describes elements that are not there.
		return []any{}
	case module.TypeMap:
		out := make(map[string]any, len(f.Fields))
		// Unenumerated keys stay absent: fieldType leaves such a map open at
		// types.Any, so `meta.whatever` is nil and must read as nil.
		for _, sub := range f.Fields {
			out[sub.Name] = zero(sub)
		}
		return out
	default:
		// Checked as Any, so there is no shape to supply.
		return nil
	}
}

// name is how a field is referred to in an error. A list's element declaration
// carries no name of its own, so it is described by position at the call site
// instead.
func name(f module.FieldDecl) string {
	if f.Name == "" {
		return "element"
	}
	return f.Name
}

// wrongType is the complaint for a value that contradicts its declaration.
//
// It names the field, what the declaration promised, and what actually arrived,
// because all three are needed to find the producer at fault — and the reader of
// this message is usually a rule author who did not write the producer.
func wrongType(f module.FieldDecl, want string, carried any) error {
	return fmt.Errorf("field %q is declared %s but the event carried %T",
		name(f), want, carried)
}
