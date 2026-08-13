package guardrail

import (
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	"github.com/sloprail/sloprail/internal/event"
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

// CompileMatcher prepares a matcher expression. An empty expression matches
// every occurrence, which is what a binding with no matcher means.
func CompileMatcher(src string) (*Matcher, error) {
	if src == "" {
		return &Matcher{}, nil
	}
	program, err := expr.Compile(src, expr.AsBool())
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
