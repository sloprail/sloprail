// Package natures holds the state a rule of any nature reads about the world
// around it — the `context` and `gates` maps a file-guard, a gate, or a context
// trigger sees when its `match` runs.
//
// It sits below the rule natures rather than inside any one of them because all
// three read the same two maps. The spec makes this explicit: a file-guard's
// `match` reads `context[<name>]`, a gate's does, a context's does, and a
// context's own `exit` and a gate's `checks` read them too — so a single
// `{active, payload}` and a single `{status}` are the shapes every one of them
// reasons over, not shapes owned by whichever nature happens to be first to
// need them. Defining them here is what lets the scope builders in
// internal/guardrail and the declaration loader agree on one representation
// instead of each inventing its own.
//
// The engine's expression language is kept out of this package on purpose, the
// same way internal/module keeps it out: these are the domain facts, and how
// they translate into an expr type environment is internal/guardrail's concern
// (see scopes.go there). A slice that loads declarations wants these structs
// without wanting expr, and this boundary is what lets it have them.
//
// The json tags are part of the contract, not decoration. A rule reads a
// context's state as `context[<name>].active` and `context[<name>].payload`,
// and a gate's as `gates[<name>].status` — the lowercase field spellings the
// spec uses. These structs cross a process boundary as JSON (one binary writes
// the state, another reads it into the map a matcher sees), so the wire field
// names must be exactly those the expression reads, which the tags fix against
// Go's default capitalised export names.
package natures

// ContextState is one context's entry in the `context` map — `context[<name>]`.
//
// Faithful to the spec's ContextState (dot-dir-file-store/main.tsp): `active` is
// whether the engine currently considers this context entered, and `payload` is
// what its `enter` script last extracted.
//
// The pair is kept rather than a bare payload because a context's LAST payload
// still matters after it goes inactive: a guard or a later check can read what
// an eval loop last measured even once that loop's context has closed. So
// `active == false` does not mean `payload` is empty, and the two are separate
// questions a rule may ask independently.
type ContextState struct {
	// Active is whether this context is currently entered.
	Active bool `json:"active"`

	// Payload is what `enter` last wrote — a flat object of whatever the
	// context chose to carry. Kept as a map because it is the context's own
	// business what it holds, the same reason an event's fields are a map: the
	// engine reads `context[<name>].payload.<key>` without knowing the keys.
	Payload map[string]any `json:"payload"`
}

// GateState is one gate's entry in the `gates` map — `gates[<name>]`.
//
// Faithful to the spec's GateState: a single `status`, `pass` or `fail`,
// carrying a gate's most recent verdict to whatever reads it. Symmetric to
// ContextState — a context can read every gate's state the same way a gate reads
// every context's.
//
// A struct with one field rather than a bare status, because the spec models it
// as one and a real case is what would widen it — a gate that later carries why
// it failed, say — and a struct is what grows without every reader changing.
type GateState struct {
	// Status is the gate's most recent verdict.
	Status GateStatus `json:"status"`
}

// GateStatus is a gate's verdict: it passed, or it failed.
//
// Kept to the spec's minimum, pass | fail, matching what a gate already does —
// block or not. Widened only against a real case, not speculatively.
type GateStatus string

const (
	// GateStatusPass is a gate that admitted the action.
	GateStatusPass GateStatus = "pass"

	// GateStatusFail is a gate that blocked it.
	GateStatusFail GateStatus = "fail"
)
