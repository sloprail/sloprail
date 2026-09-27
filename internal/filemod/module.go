// Package filemod is the module for files: what is about to be written, and
// what a cycle turned out to have changed.
package filemod

import (
	"github.com/sloprail/sloprail/internal/grounding"
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
//
// These are the spec's own names (events/main.tsp): a file event names what a
// rule asks about, and a rule about a write asks about the bytes before and
// after it, not about "content" and "result". The old `content`/`result` pair
// said one meaning under two names that a rule binding creates and updates
// alike could not choose between; `oldContent`/`newContent` say the same thing
// under names that mean the same on every kind, so one matcher over both is
// unambiguous.
const (
	FieldPath = "path"

	// FieldNewContent is what a write would leave behind — the created body on a
	// create, the post-edit bytes on an update. Required and always present on a
	// create (the file cannot be read off disk yet); optional on PreFileUpdate,
	// where a command line may not let the engine compute it — see
	// FieldResultKnown for how that gap is made askable.
	FieldNewContent = "newContent"

	// FieldOldContent is the file's bytes BEFORE the change — the current file on
	// disk for a Pre update or delete, and the session baseline's content for a
	// Post update or delete (the prior bytes are no longer on disk to be read).
	// Never carried on a create: nothing preceded it.
	FieldOldContent = "oldContent"

	// FieldNewMarkers are the `sr:` markers the written result would carry — the
	// markers of FieldNewContent. Carried on a create and an update, never on a
	// delete (nothing remains to read markers from).
	FieldNewMarkers = "newMarkers"

	// FieldOldMarkers are the `sr:` markers the file carries NOW, before the
	// change. Carried on an update and a delete, never on a create (there was no
	// prior file to read them from).
	FieldOldMarkers = "oldMarkers"

	// FieldResultKnown says whether FieldNewContent on PreFileUpdate was computed
	// or is a zero value standing in for "the engine could not work it out".
	//
	// It is not a spec field. The spec carries the gap as FieldNewContent being
	// OPTIONAL — absent when a command line does not say what bytes will result —
	// and its own doc states the requirement this field meets: "absent means the
	// engine could not know the result, which a check must tell apart from an
	// empty result, since the two imply opposite verdicts". The engine cannot
	// carry that distinction in absence alone: Matcher.env fills a DECLARED field
	// the event omitted with its type's zero value, deliberately (an absent
	// declared field used to error, matcher errors refuse, and a rule failed
	// closed on the engine's gap rather than on the author's mistake). So an
	// omitted `newContent` reads as `""`, indistinguishable from a write that
	// genuinely empties the file — which is the rule an author writes to catch
	// exactly that. A boolean beside the value is what separates them:
	// `newContent == ""` asks about the bytes, `resultKnown` asks whether the
	// engine knew them. It is declared on the two PRE kinds whose result can
	// arrive both ways — PreFileUpdate (a command-derived update) and
	// PreFileCreate (a notebook create, whose one-cell `new_source` is not the
	// document's bytes) — and nowhere else: a delete has no result, and the Post
	// kinds are settled, their bytes read off disk.
	FieldResultKnown = "resultKnown"

	// FieldSeen is true on a Post file event when an earlier Stop was already
	// handed this file with the same content: the event is a re-send, not a
	// change since the previous Stop. The Post events are the tree difference
	// against the session's baseline, so a file stays in it — and is delivered
	// on every Stop — until committed; `seen` is how a rule tells the two apart.
	// Declared on the three Post kinds. This module always emits it false: which
	// Stop saw what is the session's knowledge, and the service sets it (see
	// services/sr-session/seen.go).
	FieldSeen = "seen"

	// Keys within one entry of a markers list. Not fields of the kind: a matcher
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
	oldContent := module.FieldDecl{Name: FieldOldContent, Type: module.TypeString}
	newContent := module.FieldDecl{Name: FieldNewContent, Type: module.TypeString}
	resultKnown := module.FieldDecl{Name: FieldResultKnown, Type: module.TypeBool}
	seen := module.FieldDecl{Name: FieldSeen, Type: module.TypeBool}

	// citations are the resolved citations the action was grounded in — on
	// every kind, because a rule that requires a change be grounded asks it of
	// the change before it lands and of the file after. This module always
	// emits it empty: which citations ground a change is read off the command
	// that makes it, by the session (services/sr-session/grounding.go).
	citations := grounding.CitationsDecl()

	// A markers list declares its element's shape, and that is the whole point
	// of the Elem field. A list whose Elem is nil has its collection checked and
	// its predicate body left alone (see module.FieldDecl.Elem), so
	// `any(newMarkers, .knid == "docs")` — a typo INSIDE the predicate — would
	// compile, load, and return admitted=false forever. That is the silent
	// never-fires CompileMatcherFor exists to prevent, alive one level down.
	// With the element named, the checker refuses it at load and names the
	// fields there are.
	//
	// Elem is a TypeMap because that is what fieldType resolves to a closed
	// structure over: a list of maps whose keys are enumerated. Naming them is
	// this module asserting these are the fields, which is what makes refusing
	// the others fair.
	//
	// Two markers fields now — `oldMarkers` and `newMarkers` — with the same
	// element shape, so a rule reading either reads the same three keys. A
	// factory rather than one shared value, because each carries its own field
	// name.
	markersDecl := func(fieldName string) module.FieldDecl {
		return module.FieldDecl{
			Name: fieldName,
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
	}
	oldMarkers := markersDecl(FieldOldMarkers)
	newMarkers := markersDecl(FieldNewMarkers)

	return []module.KindDecl{
		// Pre kinds are predictions: what a tool call or a parsed command says
		// it is about to do. Refusing one prevents the work.
		{
			Name: KindPreCreate,
			// newContent and its newMarkers, PLUS resultKnown. The file does not
			// exist yet, so a rule that wants to look at what would be written has
			// nowhere else to look; on the other kinds it is already on disk. There
			// is no oldContent or oldMarkers on a creation — nothing preceded it.
			//
			// resultKnown was ONCE argued unnecessary here on the premise that "a
			// create is emitted only when the resulting bytes are known" — but that
			// premise is FALSE for a write tool whose result is not derivable. A
			// NotebookEdit creating a fresh .ipynb names a real file with a real
			// pending write, but `new_source` is one cell, not the JSON document, so
			// the resulting bytes are NOT derivable (see resultFor's notebook case).
			// That create is emitted with `newContent: ""` — indistinguishable, on
			// the value alone, from a write that genuinely creates an empty file,
			// because Matcher.env fills an absent declared field with its zero value.
			// resultKnown is the boolean beside the value that tells the two apart,
			// exactly as it does on PreFileUpdate: `newContent == ""` asks about the
			// bytes, `resultKnown` asks whether the engine knew them. Without it a
			// preventive file-guard could not fail closed on an underivable create —
			// it would judge the empty string as if it were the file and false-pass.
			Fields: []module.FieldDecl{path, newContent, resultKnown, newMarkers, citations},
		},

		// PreFileUpdate carries the bytes before AND after the change: the file
		// on disk is oldContent, and the post-edit bytes are newContent.
		//
		// newContent used to be absent — the file is on disk, so a rule could
		// read it there. But that is the file BEFORE the write, which is the
		// wrong question for the rule anyone actually writes. "Will the result
		// still have frontmatter?" cannot be answered from the bytes about to be
		// replaced.
		//
		// # Why newContent is paired with resultKnown
		//
		// The spec declares newContent OPTIONAL here (absent for a command whose
		// result the engine cannot compute) and states the requirement that
		// creates: "absent means the engine could not know the result, which a
		// check must tell apart from an empty result". The engine cannot carry
		// that in absence alone. Matcher.env fills a declared field the event
		// omitted with its type's zero value, so "we could not compute the
		// result" and "the result is the empty file" are the SAME OBSERVATION to
		// every matcher — exactly the collision that made `content: ""` on an
		// Edit-create a defect. resultKnown is the boolean beside the value that
		// makes the gap VISIBLE and askable. It is declared here and nowhere
		// else, because this is the one kind whose result genuinely arrives both
		// ways.
		//
		// # What a rule author does with it
		//
		//	resultKnown && !(newContent contains "---")   refuse a write that
		//	                                              would strip the
		//	                                              frontmatter, and say
		//	                                              nothing where the engine
		//	                                              cannot see
		//	!resultKnown                                  catch the underivable
		//	                                              cases deliberately
		//
		// The first is the shape a correct rule takes: guard on `resultKnown`,
		// then read `newContent`. A rule that reads `newContent` without guarding
		// gets the empty string on the underivable cases, which is stated here so
		// it is a choice rather than a surprise.
		//
		// oldMarkers are the markers the file carries NOW; newMarkers are the
		// markers the result would carry (empty when the result is unknown).
		{Name: KindPreUpdate, Fields: []module.FieldDecl{path, oldContent, newContent, resultKnown, oldMarkers, newMarkers, citations}},

		// A delete carries the bytes about to be lost (oldContent) and the
		// markers that go with them (oldMarkers), and nothing about a result —
		// nothing remains. No newMarkers for the same reason: a deletion has no
		// text to read them out of.
		{Name: KindPreDelete, Fields: []module.FieldDecl{path, oldContent, oldMarkers, citations}},

		// Post kinds are observations, established by comparing the tree against
		// where the session started rather than by trusting what any action
		// announced. Refusing one demands a correction.
		//
		// Each Post kind mirrors its Pre counterpart's shape — with both contents
		// present on an update, since the change has settled and neither had to
		// be predicted, so there is no resultKnown here. oldContent comes from
		// the session baseline (the prior bytes are no longer on disk); newContent
		// is read from disk as it now sits.
		{Name: KindPostCreate, Fields: []module.FieldDecl{path, newContent, newMarkers, seen, citations}},
		{Name: KindPostUpdate, Fields: []module.FieldDecl{path, oldContent, newContent, oldMarkers, newMarkers, seen, citations}},
		{Name: KindPostDelete, Fields: []module.FieldDecl{path, oldContent, oldMarkers, seen, citations}},
	}
}
