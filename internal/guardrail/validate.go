package guardrail

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sloprail/sloprail/internal/module"
)

// Validate reports everything wrong with a declaration, given the kinds this
// build can actually produce.
//
// Every problem, not the first. An author who fixes one fault and reloads only
// to meet the next has paid a round trip per mistake, and the mistakes in a
// declaration are usually the same mistake repeated — one misremembered field
// name across three bindings. Reporting them together is what makes the load
// check something an author uses rather than endures.
//
// An empty result means the declaration can do what it says: every kind it
// binds to exists, every matcher reads fields that kind carries, and every hook
// names a mechanism we have and a command we can run.
func Validate(d Declaration, reg *module.Registry) []Problem {
	if !d.IsEnabled() {
		// A rule switched off is not a rule that has to be correct. Turning one
		// off is a declaration rather than a deletion precisely so a rule can be
		// parked — half-written, or superseded, or waiting on a module that has
		// not shipped — without the reasoning that produced it being thrown
		// away. Reporting it at every session start would make parking one cost
		// a warning forever, and the way to silence that would be deletion.
		return nil
	}

	if len(d.Hooks) == 0 {
		// A declaration binding nothing enforces nothing, which is a legitimate
		// thing to have written down — a rule documented before it is wired up.
		return nil
	}

	var problems []Problem

	// Sorted, so two runs over the same declaration report in the same order.
	// Map iteration would make the output shuffle between loads and turn a
	// diff of two reports into noise.
	for _, kind := range sortedKinds(d.Hooks) {
		decl, known := reg.KindDeclFor(kind)
		if !known {
			problems = append(problems, at(ErrUnknownEventKind, kind, -1, -1,
				"no module produces it — %s", available(reg)))
			// No declaration to check the matchers against. Reporting each of
			// them as naming unknown fields would bury the one fault that
			// explains all of them.
			continue
		}

		for i, b := range d.Hooks[kind] {
			problems = append(problems, validateBinding(d, b, kind, decl, i)...)
		}
	}

	return problems
}

// validateBinding checks one binding: its matcher against the kind's fields,
// and each of its hooks against what the engine can run.
func validateBinding(d Declaration, b Binding, kind string, decl module.KindDecl, i int) []Problem {
	var problems []Problem

	if _, err := CompileMatcherFor(b.Matcher, decl); err != nil {
		problems = append(problems, at(ErrBadMatcher, kind, i, -1,
			"%s — %s carries %s", oneLine(err.Error()), kind, fieldList(decl)))
	}

	if len(b.Hooks) == 0 {
		// A binding with no hooks fires nothing. Distinct from a declaration
		// that binds nothing: this one named an event and a matcher, so it
		// reads as enforcing something and does not.
		problems = append(problems, at(ErrNoHooks, kind, i, -1,
			"no hooks — this binding can never do anything"))
	}

	for j, h := range b.Hooks {
		problems = append(problems, validateHook(d, h, kind, i, j)...)
	}
	return problems
}

// available names the kinds this build can produce, so an author who
// mistyped one can see the one they meant. A build with no modules is worth
// saying outright rather than as an empty list, since it points at the engine
// rather than at the declaration.
func available(reg *module.Registry) string {
	kinds := reg.DeclaredKinds()
	if len(kinds) == 0 {
		return "this build produces no events at all"
	}
	return "this build has " + strings.Join(kinds, ", ")
}

// fieldList names what a kind carries, so a refused matcher says what it could
// have said instead. A misspelling is obvious once the right spelling is in
// front of the author, and invisible until then.
func fieldList(decl module.KindDecl) string {
	if len(decl.Fields) == 0 {
		return "no fields"
	}
	names := make([]string, 0, len(decl.Fields))
	for _, f := range decl.Fields {
		names = append(names, fmt.Sprintf("%s (%s)", f.Name, f.Type))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func sortedKinds(m map[string][]Binding) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
