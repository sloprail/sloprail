package filemod

import (
	"fmt"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/grounding"
)

// FileEvent is what this module's own code passes around.
//
// Typed here, a map at the boundary. The engine has no use for a struct it
// cannot know the shape of, and this module has no use for a map when it is
// the one that put the values in.
type FileEvent struct {
	// Path is relative to the repository root.
	Path string

	// OldContent is the file's bytes BEFORE the change — the file on disk for a
	// Pre update or delete, and the session baseline for a Post update or delete.
	// Empty on a create: nothing preceded it, and the create kinds do not declare
	// it, so an empty value here is never carried on one.
	OldContent string

	// NewContent is what a write would leave behind — the created body on a
	// create, the post-edit bytes on an update. On a Pre update it is meaningful
	// only alongside ResultKnown.
	NewContent string

	// ResultKnown says whether NewContent on a Pre update was computed or is a
	// zero value standing in for "the engine could not work it out".
	//
	// Carried as its own field rather than inferred from NewContent being empty,
	// because an action can legitimately produce an empty file — which is the
	// same collision, one level up, that this whole change removes. Only
	// PreFileUpdate declares it.
	ResultKnown bool

	// OldMarkers are the `sr:` annotations the OLD text carries. Set on the kinds
	// that have prior text to read — the Pre/Post update and delete kinds.
	OldMarkers []Marker

	// NewMarkers are the `sr:` annotations the NEW text carries — the markers of
	// NewContent. Set on the create and update kinds; a delete has none.
	NewMarkers []Marker

	// Seen says a Post event re-sends a file an earlier Stop was already handed
	// with this content. See FieldSeen; set by the session, not this module.
	Seen bool
}

// Event converts to the wire form under the given kind.
//
// Every field is driven by the DECLARATION rather than by whether its value is
// empty: kindDeclares decides which fields a kind carries, and the value decides
// only what they hold. A markers field is carried as an empty list on a kind
// that declares it even when the text has no markers, because `len(newMarkers)
// == 0` is the rule an author writes for unmarked code, and a field that
// vanished when it was empty would make that rule error rather than hold. The
// same holds for content: a genuinely empty `newContent` must still be present
// so `newContent == ""` fires rather than errors.
func (f FileEvent) Event(kind string) event.Event {
	// Path is unconditional, and it is the one field that must be.
	//
	// Every kind this module declares carries it — TestFileEvent_PathSurvives-
	// AnUnknownKind holds that — so for a known kind this is what the
	// declaration says anyway. The difference is an UNKNOWN kind, where
	// kindDeclares answers false for everything: keying path off the
	// declaration there produced an event with no fields at all, losing the
	// path of the file it was reporting. Event does not police the kind (the
	// registry does), so a kind it does not recognise must still name its file
	// rather than describe nothing.
	fields := map[string]any{FieldPath: f.Path}
	if kindDeclares(kind, FieldOldContent) {
		fields[FieldOldContent] = f.OldContent
	}
	if kindDeclares(kind, FieldNewContent) {
		fields[FieldNewContent] = f.NewContent
	}
	// resultKnown appears only where it is declared (PreFileUpdate) and is
	// unconditional there — a value that appeared only when the result was known
	// would be indistinguishable from one that was known to be empty, since
	// Matcher.env fills an absent declared field with its zero value, which is the
	// defect this pair exists to avoid.
	if kindDeclares(kind, FieldResultKnown) {
		fields[FieldResultKnown] = f.ResultKnown
	}
	if kindDeclares(kind, FieldSeen) {
		fields[FieldSeen] = f.Seen
	}
	if kindDeclares(kind, FieldOldMarkers) {
		fields[FieldOldMarkers] = markerFields(f.OldMarkers)
	}
	if kindDeclares(kind, FieldNewMarkers) {
		fields[FieldNewMarkers] = markerFields(f.NewMarkers)
	}
	if kindDeclares(kind, grounding.FieldCitations) {
		fields[grounding.FieldCitations] = grounding.ToWire(nil)
	}
	return event.Event{Kind: kind, Fields: fields}
}

// kindDeclares reports whether a kind declares a field, read off the same list
// Kinds builds so the two cannot drift.
//
// Every field goes through here, and that uniformity is the fix rather than an
// aesthetic. The rule is: the DECLARATION decides which fields an event carries,
// and the value decides only what they hold. Deciding presence from the value
// broke it in both directions at once.
//
// Absent when it should be present: content was set only when non-empty, so
// writing a genuinely empty file produced a create with no content field —
// a kind missing a field the spec declares required. A matcher written
// `newContent == ""`, the exact rule an author writes to catch an empty file,
// then met a nil where a string was declared and ERRORED rather than firing.
// Since matcher errors now refuse (session_pre_tool.go), that turned every
// empty-file write into a refusal citing a broken guardrail.
//
// Present when it should be absent: content was set whenever it was non-empty
// whatever the kind, so a FileEvent carrying content produced a PreFileDelete
// with a content field nothing declares — a field no matcher can be checked
// against, because CompileMatcherFor validates against the declaration and
// refuses the name.
//
// Both are the same bug: the emitter answering from the value instead of the
// declaration. One rule fixes both, which is why the two are fixed together.
func kindDeclares(kind, field string) bool {
	for _, k := range (&Module{}).Kinds() {
		if k.Name != kind {
			continue
		}
		for _, f := range k.Fields {
			if f.Name == field {
				return true
			}
		}
		return false
	}
	return false
}

// FromEvent converts back, for this module's own reads of events it produced.
// An event carrying no path is an error rather than a zero value: silently
// returning an empty one would let a caller act on a file that was never named.
func FromEvent(e event.Event) (FileEvent, error) {
	f := FileEvent{}
	if v, ok := e.Fields[FieldPath].(string); ok {
		f.Path = v
	}
	if v, ok := e.Fields[FieldOldContent].(string); ok {
		f.OldContent = v
	}
	if v, ok := e.Fields[FieldNewContent].(string); ok {
		f.NewContent = v
	}
	f.Seen, _ = e.Fields[FieldSeen].(bool)
	f.OldMarkers = markersFromField(e.Fields[FieldOldMarkers])
	f.NewMarkers = markersFromField(e.Fields[FieldNewMarkers])
	if f.Path == "" {
		return FileEvent{}, fmt.Errorf("filemod: %q carries no %s", e.Kind, FieldPath)
	}
	return f, nil
}

// markersFromField reads a wire markers list back into typed Markers. A value of
// the wrong shape yields nothing rather than an error: this is a read of events
// this module itself produced, so a malformed list is not a case a caller acts
// on differently from an absent one.
func markersFromField(v any) []Marker {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]Marker, 0, len(list))
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		mk := Marker{}
		if s, ok := m[KeyMarkerKind].(string); ok {
			mk.Kind = s
		}
		if s, ok := m[KeyMarkerFQN].(string); ok {
			mk.FQN = s
		}
		if n, ok := m[KeyMarkerLine].(int); ok {
			mk.Line = n
		}
		out = append(out, mk)
	}
	return out
}
