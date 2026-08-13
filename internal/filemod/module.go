// Package filemod is the module for files: what is about to be written, and
// what a cycle turned out to have changed.
package filemod

import (
	"github.com/sloprail/sloprail/internal/module"
)

// Name identifies this module. It is how the engine reports which module
// produced an event, and how a module is switched off — not a prefix the
// kinds carry.
const Name = "file"

// The kinds this module declares. The name says what happened and when; which
// module owns it is the registry's to know, since the module registered it.
const (
	KindPreCreate  = "PreFileCreate"
	KindPreUpdate  = "PreFileUpdate"
	KindPreDelete  = "PreFileDelete"
	KindPostCreate = "PostFileCreate"
	KindPostUpdate = "PostFileUpdate"
	KindPostDelete = "PostFileDelete"
)

// Field names. They appear here, in Kinds below, and in the conversion in
// event.go — nowhere else, so a rename cannot leave a matcher checking against
// a name the events no longer carry.
const (
	FieldPath    = "path"
	FieldContent = "content"
	FieldMarkers = "markers"

	// Keys within one entry of FieldMarkers. Not fields of the kind: a matcher
	// reads them off an element of the list, and the declaration describes the
	// list itself.
	KeyMarkerKind = "kind"
	KeyMarkerFQN  = "fqn"
	KeyMarkerLine = "line"
)

// Module produces file events.
type Module struct{}

// New returns the file module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Kinds implements module.Module.
//
// Timing is in the name and nowhere else. "PreFileCreate" says when it fires;
// a field repeating that would be the same fact in two places, and two copies
// of a fact can disagree. The engine has no use for it either — it is running
// at a pre-tool hook or at the end of a cycle, and knows which without asking
// the events it is holding.
//
// The Post kinds carry the same field as their Pre counterparts on purpose. A
// rule about a file is a rule about a file whether it is checked before the
// write lands or after the cycle settles, and one hook bound to both should
// cost one script rather than two that have to be kept in step.
func (*Module) Kinds() []module.KindDecl {
	path := module.FieldDecl{Name: FieldPath, Type: module.TypeString}
	content := module.FieldDecl{Name: FieldContent, Type: module.TypeString}

	// markers declares its element's shape, and that is the whole point of the
	// Elem field. A list whose Elem is nil has its collection checked and its
	// predicate body left alone (see module.FieldDecl.Elem), so
	// `any(markers, .knid == "docs")` — a typo INSIDE the predicate — would
	// compile, load, and return admitted=false forever. That is the silent
	// never-fires CompileMatcherFor exists to prevent, alive one level down.
	// With the element named, the checker refuses it at load and names the
	// fields there are.
	//
	// Elem is a TypeMap because that is what fieldType resolves to a closed
	// structure over: a list of maps whose keys are enumerated. Naming them is
	// this module asserting these are the fields, which is what makes refusing
	// the others fair.
	markers := module.FieldDecl{
		Name: FieldMarkers,
		Type: module.TypeList,
		Elem: &module.FieldDecl{
			Type: module.TypeMap,
			Fields: []module.FieldDecl{
				{Name: KeyMarkerKind, Type: module.TypeString},
				{Name: KeyMarkerFQN, Type: module.TypeString},
				{Name: KeyMarkerLine, Type: module.TypeInt},
			},
		},
	}

	return []module.KindDecl{
		// Pre kinds are predictions: what a tool call or a parsed command says
		// it is about to do. Refusing one prevents the work.
		{
			Name: KindPreCreate,
			// Content only here. The file does not exist yet, so a rule that
			// wants to look at what would be written has nowhere else to look;
			// on the other kinds it is already on disk.
			Fields: []module.FieldDecl{path, content, markers},
		},
		{Name: KindPreUpdate, Fields: []module.FieldDecl{path, markers}},

		// No markers on a delete. A deletion has no text to read them out of,
		// so the field could only ever be empty — and an always-empty field is
		// one a rule can match on and never learn anything from. `len(markers)
		// == 0` is a real question to ask of a create or an update; asked of a
		// delete it is a tautology dressed as a rule.
		{Name: KindPreDelete, Fields: []module.FieldDecl{path}},

		// Post kinds are observations, established by comparing the tree
		// against where the session started rather than by trusting what any
		// action announced. Refusing one demands a correction.
		{Name: KindPostCreate, Fields: []module.FieldDecl{path}},
		{Name: KindPostUpdate, Fields: []module.FieldDecl{path}},
		{Name: KindPostDelete, Fields: []module.FieldDecl{path}},
	}
}
