// Package module is the extension point.
//
// A module is a domain sloprail understands — files, commands, later markers.
// It declares the events it can produce and what each carries, and produces
// them from what a harness reported. Everything else in the engine works from
// those declarations rather than from anything compiled into it, so adding a
// domain is adding a module rather than editing the parts that route, match and
// dispatch.
package module

import "github.com/sloprail/sloprail/internal/event"

// Module is a domain and the events it produces.
type Module interface {
	// Name prefixes every kind this module produces: "file" owns "file.*".
	// Two modules can then be written by people who never met without having
	// to agree on which of them owns "created".
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
	// Name is unqualified — "pre_create", not "file.pre_create". The registry
	// applies the prefix, so a module cannot claim a name outside its own
	// namespace even by accident.
	Name string

	// Fields a matcher may read on this kind.
	Fields []FieldDecl
}

// FieldDecl is one field on a kind.
type FieldDecl struct {
	Name string
	Type FieldType
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
