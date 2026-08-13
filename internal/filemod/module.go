// Package filemod is the module for files: what is about to be written, and
// what a cycle turned out to have changed.
package filemod

import "github.com/sloprail/sloprail/internal/module"

// Name is this module's namespace. Every kind it produces is prefixed with it.
const Name = "file"

// Kind names, unqualified. Qualified they read "file.pre_create".
const (
	KindPreCreate  = "pre_create"
	KindPreUpdate  = "pre_update"
	KindPreDelete  = "pre_delete"
	KindPostCreate = "post_create"
	KindPostUpdate = "post_update"
	KindPostDelete = "post_delete"
)

// Field names. They appear here, in Kinds below, and in the conversion in
// event.go — nowhere else, so a rename cannot leave a matcher checking against
// a name the events no longer carry.
const (
	FieldPath    = "path"
	FieldContent = "content"
)

// Module produces file events.
type Module struct{}

// New returns the file module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Kinds implements module.Module.
//
// Timing is in the name and nowhere else. "file.pre_create" says when it fires;
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

	return []module.KindDecl{
		// Pre kinds are predictions: what a tool call or a parsed command says
		// it is about to do. Refusing one prevents the work.
		{
			Name: KindPreCreate,
			// Content only here. The file does not exist yet, so a rule that
			// wants to look at what would be written has nowhere else to look;
			// on the other kinds it is already on disk.
			Fields: []module.FieldDecl{path, content},
		},
		{Name: KindPreUpdate, Fields: []module.FieldDecl{path}},
		{Name: KindPreDelete, Fields: []module.FieldDecl{path}},

		// Post kinds are observations, established by comparing the tree
		// against where the session started rather than by trusting what any
		// action announced. Refusing one demands a correction.
		{Name: KindPostCreate, Fields: []module.FieldDecl{path}},
		{Name: KindPostUpdate, Fields: []module.FieldDecl{path}},
		{Name: KindPostDelete, Fields: []module.FieldDecl{path}},
	}
}
