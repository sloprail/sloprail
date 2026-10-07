// Package cyclemod is the module for the cycle itself: that one ended.
//
// A module rather than a constant in the dispatcher, because everything the
// engine does with an event kind works from a module's declaration. A kind no
// module declares cannot be bound to at all — the loader rejects the binding
// with "no module produces it" — so a dispatcher emitting Stop without one
// would fire an event no guardrail in the project is permitted to name. That is
// not a theoretical gap: it is the state this package was added to fix, and it
// is invisible from the dispatcher, which happily emits an event nothing can
// receive.
package cyclemod

import (
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
)

// Name identifies this module.
const Name = "cycle"

// KindStop says a work cycle ended — the harness-native Stop moment.
//
// Named `Stop`, not `TurnEnd`: the harness's own Stop hook already covers the
// end of a cycle, so there is no separate semantic end-of-cycle event to
// maintain alongside it. See events/main.tsp, EventKind.Stop.
const KindStop = "Stop"

// Module produces the cycle's own events.
type Module struct{}

// New returns the cycle module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Kinds implements module.Module.
//
// One kind, carrying NO fields. That absence is the declaration: the end of a
// cycle is about the cycle rather than about one file, which is why what
// changed is reported as its own event per file rather than as a list this one
// carries. A matcher narrowing on a subject therefore has nothing to narrow on,
// and a rule bound to this runs for the cycle as a whole.
//
// Declaring the fields as empty is what makes that enforceable rather than
// merely intended. A matcher naming `path` on this kind is refused when the
// guardrail loads, with the same message a misspelled field on any other kind
// gets — instead of loading, evaluating against nothing, and never firing.
func (*Module) Kinds() []module.KindDecl {
	return []module.KindDecl{{Name: KindStop}}
}

// Extract implements module.Module.
//
// Nothing. This module produces no events from what a harness reported, because
// a cycle ending is not something a harness reports as an action — it is the
// hook point itself, and the dispatcher is what knows the cycle reached its
// end. What this module contributes is the DECLARATION: that the kind exists,
// what it carries, and therefore that a guardrail may bind to it.
//
// Which is why returning nothing is not this module being unfinished. The
// alternative — a module manufacturing a Stop whenever it was asked — would
// emit one at the pre-tool point too, where no cycle has ended.
func (*Module) Extract(module.Input) ([]event.Event, error) { return nil, nil }

// Event is the one event this module's kind describes.
//
// Built here rather than in the dispatcher so that the kind's name and its
// emptiness are stated in one place. A dispatcher assembling the event itself
// could give it fields the declaration says it has none of, and nothing would
// catch that: the matcher is compiled against the declaration, so an extra
// field would simply be unreadable and unmentioned.
// sr:invariant events/turn-end-every-turn
func Event() event.Event {
	return event.Event{Kind: KindStop, Fields: map[string]any{}}
}
