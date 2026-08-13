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
	fields := map[string]any{FieldPath: f.Path}
	if f.Content != "" {
		fields[FieldContent] = f.Content
	}
	if kindCarriesMarkers(kind) {
		fields[FieldMarkers] = markerFields(f.Markers)
	}
	return event.Event{Kind: kind, Fields: fields}
}

// kindCarriesMarkers reports whether a kind declares the markers field, read
// off the same list Kinds builds so the two cannot drift.
//
// Scoped to markers, and the scope is not modesty. The content field above does
// NOT work this way — it is set whenever FileEvent.Content is non-empty, kind
// be damned, so a FileEvent carrying content produces a PreFileDelete with a
// content field nothing declares. That is pre-existing and untouched here; see
// TestFileEvent_ContentLeaksOntoKindsThatDoNotDeclareIt, which pins it so the
// difference between the two paths is recorded rather than assumed away.
func kindCarriesMarkers(kind string) bool {
	for _, k := range (&Module{}).Kinds() {
		if k.Name != kind {
			continue
		}
		for _, f := range k.Fields {
			if f.Name == FieldMarkers {
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
