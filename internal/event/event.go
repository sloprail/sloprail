// Package event is what crosses every boundary in sloprail.
//
// An event says something happened, or is about to. What kind of thing, and
// what it carries, belongs to whichever module produced it — this package
// defines only the envelope, so that a module added later can bring kinds and
// fields nothing here has seen.
package event

import "encoding/json"

// Event is one thing that happened, or is about to.
//
// Two fields, because two is what the engine needs: the kind, to decide which
// guardrails care, and Fields, to hand to a matcher. The engine never learns
// what a "path" is. That a file event has one is the file module's business,
// and asking the engine to know would mean every new module editing it.
type Event struct {
	// Kind is what happened — "PreFileCreate". Which module owns it is not
	// encoded here: the registry knows, because the module registered it, and
	// a name that also had to carry its owner would be answering by spelling
	// what a lookup already answers.
	Kind string `json:"kind"`

	// Fields is what a matcher reads and what a hook receives. The names are
	// the module's, declared alongside the kind so a matcher naming a field
	// that does not exist is caught when the guardrail loads rather than
	// failing silently at the moment it should have fired.
	//
	// A subjectless event carries no fields, and on the wire that is an empty
	// object rather than null — see MarshalJSON.
	Fields map[string]any `json:"fields"`
}

// MarshalJSON writes an event with `fields` always an object.
//
// A nil map marshals to null by default, so Stop — which declares no fields
// at all — reached a hook as `"fields":null`. A hook doing the obvious thing
// with it, `.fields.path` in jq or `payload["event"]["fields"].get("path")` in
// Python, gets an error on null where it gets a clean miss on `{}`. The hook
// then exits non-zero, and a non-zero exit is a refusal: an event carrying
// nothing would have refused the work it was reporting on.
//
// Not `omitempty`, which drops the key entirely and moves the same failure one
// step earlier — a hook reading `.fields` would find nothing rather than
// nothing-in-particular. The shape a hook can rely on is the key always being
// there and always being an object.
func (e Event) MarshalJSON() ([]byte, error) {
	// A named type without the method, so this does not recurse.
	type wire Event
	if e.Fields == nil {
		e.Fields = map[string]any{}
	}
	return json.Marshal(wire(e))
}
