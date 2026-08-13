package filemod

import (
	"fmt"

	"github.com/sloprail/sloprail/internal/event"
)

// FileEvent is what this module's own code passes around.
//
// Typed here, a map at the boundary. The engine has no use for a struct it
// cannot know the shape of, and this module has no use for a map when it is
// the one that put the values in.
type FileEvent struct {
	// Path is relative to the repository root.
	Path string

	// Content is what would be written. Set on PreFileCreate alone — anywhere
	// else the file is on disk and a hook can read it there rather than
	// having it copied through every event.
	Content string

	// Markers are the `sr:` annotations the text carries. Set on the two Pre
	// kinds that have text to read; see Kinds for why a delete has none.
	Markers []Marker
}

// Event converts to the wire form under the given kind.
//
// Markers are carried on the kinds that declare them and omitted from the rest,
// keyed off the declaration rather than off whether the slice is empty: a create
// of a file with no markers must still carry `markers` as an empty list, because
// `len(markers) == 0` is the rule an author writes for unmarked code, and a
// field that vanished when it was empty would make that rule error rather than
// hold.
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
	if kindDeclares(kind, FieldContent) {
		fields[FieldContent] = f.Content
	}
	if kindDeclares(kind, FieldMarkers) {
		fields[FieldMarkers] = markerFields(f.Markers)
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
// writing a genuinely empty file produced a PreFileCreate with no `content` —
// a kind missing a field the spec declares required (events/main.tsp:79). A
// matcher written `content == ""`, the exact rule an author writes to catch an
// empty file, then met a nil where a string was declared and ERRORED rather
// than firing. Since matcher errors now refuse (session_pre_tool.go), that
// turned every empty-file write into a refusal citing a broken guardrail.
//
// Present when it should be absent: content was set whenever it was non-empty
// whatever the kind, so a FileEvent carrying content produced a PreFileDelete
// with a `content` field nothing declares — a field no matcher can be checked
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
	if v, ok := e.Fields[FieldContent].(string); ok {
		f.Content = v
	}
	if v, ok := e.Fields[FieldMarkers].([]any); ok {
		f.Markers = make([]Marker, 0, len(v))
		for _, entry := range v {
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
			f.Markers = append(f.Markers, mk)
		}
	}
	if f.Path == "" {
		return FileEvent{}, fmt.Errorf("filemod: %q carries no %s", e.Kind, FieldPath)
	}
	return f, nil
}
