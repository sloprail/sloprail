// Package event is what crosses every boundary in sloprail.
//
// An event says something happened, or is about to. What kind of thing, and
// what it carries, belongs to whichever module produced it — this package
// defines only the envelope, so that a module added later can bring kinds and
// fields nothing here has seen.
package event

// Event is one thing that happened, or is about to.
//
// Two fields, because two is what the engine needs: the kind, to decide which
// guardrails care, and Fields, to hand to a matcher. The engine never learns
// what a "path" is. That a file event has one is the file module's business,
// and asking the engine to know would mean every new module editing it.
type Event struct {
	// Kind is namespaced by the module that produces it — "file.pre_create".
	// The prefix is what lets two modules coexist without agreeing on names,
	// and what lets the engine tell whose event it is holding without asking.
	Kind string `json:"kind"`

	// Fields is what a matcher reads and what a hook receives. The names are
	// the module's, declared alongside the kind so a matcher naming a field
	// that does not exist is caught when the guardrail loads rather than
	// failing silently at the moment it should have fired.
	Fields map[string]any `json:"fields"`
}

// Module returns the name of the module that produced this event — the part of
// the kind before the dot. An event whose kind carries no namespace belongs to
// nobody, and returns the empty string.
func (e Event) Module() string {
	for i := 0; i < len(e.Kind); i++ {
		if e.Kind[i] == '.' {
			return e.Kind[:i]
		}
	}
	return ""
}
