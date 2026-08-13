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
	return compile(src, expr.Env(matcherEnv(kind)))
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
func (m *Matcher) env(e event.Event) map[string]any {
	env := make(map[string]any, len(e.Fields))
	for k, v := range e.Fields {
		env[k] = v
	}
	return env
}
