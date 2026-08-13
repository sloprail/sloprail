// Package module is the extension point.
//
// A module is a domain sloprail understands — files, commands, later markers.
// It declares the events it can produce and what each carries, and produces
// them from what a harness reported. Everything else in the engine works from
// those declarations rather than from anything compiled into it, so adding a
// domain is adding a module rather than editing the parts that route, match and
// dispatch.
//
// # Adding one
//
// Write a type satisfying Module, anywhere, named anything — nothing in this
// package or the tests around it cares where it lives or what its package is
// called, and Name() is the module's own and need not resemble either. Then add
// it to modules.All in internal/module/modules. That is the whole procedure,
// and the second step is not optional: a module absent from that list is dead
// code, and internal/module/modules.TestAll_HoldsEveryModuleInTheRepo fails
// until it is there. That test finds your type by type-checking the repo for
// implementations of Module, so it will find it wherever you put it.
//
// There is exactly one module list in a build. NewRegistry takes a token only
// packages under internal/module/ can name, and which no other package can
// construct without naming it, so modules.Registry is the only registry a hook
// point can be handed — assembling a different one elsewhere does not compile.
// See internal/module/internal/registryauth.
package module

import "github.com/sloprail/sloprail/internal/event"

// Module is a domain and the events it produces.
type Module interface {
	// Name identifies the module — how the engine reports what produced an
	// event, and how a module is referred to when one is switched off.
	Name() string

	// Kinds are the events this module can produce, with the fields each
	// carries.
	//
	// Declared rather than discovered, so that a matcher naming a field the
	// kind does not have is refused when the guardrail loads. A rule that
	// silently never fires is worse than one that will not load: the first
	// looks like a rule being satisfied.
	Kinds() []KindDecl

	// Extract turns what a harness reported into events. Producing none is
	// ordinary — most of what happens in a session concerns most modules not
	// at all.
	Extract(Input) ([]event.Event, error)
}

// KindDecl is one event kind and what it carries.
type KindDecl struct {
	// Name is what happened, and when — "PreFileCreate". It does not say which
	// module produced it: the registry knows that because the module declared
	// it, and it rejects a second module claiming the same name.
	Name string

	// Fields a matcher may read on this kind.
	Fields []FieldDecl
}

// FieldDecl is one field on a kind.
type FieldDecl struct {
	Name string
	Type FieldType

	// Elem describes what a TypeList holds, when the module can say. Ignored
	// for every other type.
	//
	// It exists because the matchers that read a list read inside it:
	//
	//   any(invocations, .bin == "npm" && "--access" in .flags)
	//
	// is the form the spec documents, and it is where a command rule lives.
	// Without an element shape the collection is checked and the predicate
	// body is not, so `.bni` inside that expression would load and never fire —
	// precisely the silence declaring fields exists to prevent, reappearing one
	// level down.
	//
	// A list whose Elem is nil is a list of something unspecified, and a
	// predicate over it is left unchecked rather than refused. That is the
	// honest answer when a module has not said: unlike a map's keys, an
	// element's fields CAN be enumerated, so a module that knows them should
	// declare them and get the check.
	Elem *FieldDecl

	// Fields describes a TypeMap's or a list element's own fields, when the
	// module can enumerate them. Nil means it cannot, and reads of arbitrary
	// keys go unchecked rather than refused.
	Fields []FieldDecl
}

// FieldType is what a matcher can expect a field to hold. Deliberately small:
// this exists to catch a misspelled field name and a comparison against the
// wrong shape, not to describe a schema.
type FieldType string

const (
	TypeString FieldType = "string"
	TypeBool   FieldType = "bool"
	TypeList   FieldType = "list"
	TypeMap    FieldType = "map"

	// TypeInt is a whole number. It exists because a field that holds one and
	// is declared as anything else is checked wrongly in both directions: as a
	// string, `.line > 10` is refused for a comparison that is correct; as
	// nothing at all, `.line == "3"` loads for a comparison that can never
	// hold. A marker's line is the first such field.
	TypeInt FieldType = "int"
)

// Input is what a module is given to extract from.
//
// A map rather than a struct for the same reason an event's fields are: what a
// module needs to answer honestly is its own business. The file module wants
// the guardrails in play, to know what it may leave out; the command module
// wants nothing but the payload. A shared struct would have to grow a field
// for each, and a module added later could not grow it at all.
type Input map[string]any

// Well-known input keys. A module reads what it needs and ignores the rest.
const (
	// InputPayload is what the harness reported, as it reported it.
	InputPayload = "payload"

	// InputPhase is which hook point is asking: what a cycle is about to do,
	// or what it turned out to have done.
	InputPhase = "phase"
)

// Phase values for InputPhase.
const (
	PhasePre  = "pre"
	PhasePost = "post"
)
