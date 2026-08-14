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

	// FieldResult is the bytes the file will hold AFTER the pending action, on
	// the kinds where the engine could work them out. See KindPreUpdate's
	// declaration for why this is a second field rather than `content` growing
	// a second meaning, and FieldResultKnown for how "could not work them out"
	// is said.
	FieldResult = "result"

	// FieldResultKnown says whether FieldResult was computed or defaulted.
	//
	// It exists because absence cannot say it. Matcher.env fills a DECLARED
	// field the event omitted with its type's zero value, deliberately — an
	// absent declared field used to error, matcher errors refuse, and a rule
	// failed closed on the engine's gap rather than on the author's mistake. So
	// an omitted `result` reads as `""`, which is indistinguishable from a
	// pending action that genuinely empties the file.
	//
	// That is the same collision this whole change exists to remove, one field
	// along. A boolean beside the value is what separates them: `result == ""`
	// asks about the bytes, and `resultKnown` asks whether the engine knew them.
	FieldResultKnown = "resultKnown"

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
	result := module.FieldDecl{Name: FieldResult, Type: module.TypeString}
	resultKnown := module.FieldDecl{Name: FieldResultKnown, Type: module.TypeBool}

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
			//
			// A create's content IS its result — there are no prior bytes for a
			// replacement to be relative to — so `result` is not declared here.
			// Declaring both would be one fact under two names, free to
			// disagree, and a rule author would have no way to choose between
			// them. `resultKnown` is likewise absent: a create is emitted only
			// when the resulting bytes are known, and a field that is always
			// true is one a rule can match on and never learn anything from —
			// the argument KindPreDelete already makes about markers.
			Fields: []module.FieldDecl{path, content, markers},
		},

		// PreFileUpdate carries the POST-EDIT bytes, and this is Q1's answer.
		//
		// It used to carry none: the file is on disk, so a rule could read it
		// there. But that is the file BEFORE the write, which is the wrong
		// question for the rule anyone actually writes. "Will the result still
		// have frontmatter?" cannot be answered from the bytes about to be
		// replaced, and markersOnDisk's own comment has recorded this as a
		// known limitation since it was written.
		//
		// # Why a new field rather than `content`
		//
		// `content` means "the bytes this action states outright", and on
		// PreFileCreate it is required and always present. Reusing it here
		// would make one name mean "the stated body" on one kind and "the
		// computed outcome" on another, and a rule bound to both kinds — which
		// is the normal case, since a guardrail about a file usually cares
		// about creates and updates alike — could not tell which it had.
		//
		// # Why the value is paired with a boolean
		//
		// This is the constraint that decided the shape, and it is the engine's
		// own. Matcher.env fills a declared field the event omitted with its
		// type's zero value. So "we could not compute the result" and "the
		// result is the empty file" are the SAME OBSERVATION to every matcher —
		// exactly the collision that made `content: ""` on an Edit-create a
		// defect worth this whole change.
		//
		// Three options were on the table. (a) Leave PreFileUpdate contentless:
		// rejected, it is the limitation named above and the owner asked for
		// the opposite. (b) Add `result` alone, present only when computable:
		// rejected, because absence is not observable — it silently reads as
		// `""`, so a `sed -i` whose outcome is unknowable would look like a
		// command that empties the file, and a rule refusing empty results
		// would fire on it. (c) `result` plus `resultKnown`: taken. The pair
		// makes the gap VISIBLE and askable, which is the property the other
		// two cannot give.
		//
		// Both are unconditional, so neither is ever filled in by the engine on
		// this kind, and `resultKnown` is a real question here in a way it is
		// not on a create: an update genuinely arrives both ways.
		//
		// # What a rule author does with it
		//
		//	resultKnown && !(result contains "---")   refuse a write that would
		//	                                          strip the frontmatter, and
		//	                                          say nothing where the
		//	                                          engine cannot see
		//	!resultKnown                              catch the underivable
		//	                                          cases deliberately
		//
		// The first is the shape a correct rule takes: guard on `resultKnown`,
		// then read `result`. A rule that reads `result` without guarding gets
		// the empty string on the underivable cases, which is stated here so it
		// is a choice rather than a surprise.
		{Name: KindPreUpdate, Fields: []module.FieldDecl{path, result, resultKnown, markers}},

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
