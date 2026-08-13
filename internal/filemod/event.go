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
}

// Event converts to the wire form under the given kind.
func (f FileEvent) Event(kind string) event.Event {
	fields := map[string]any{FieldPath: f.Path}
	if f.Content != "" {
		fields[FieldContent] = f.Content
	}
	return event.Event{Kind: kind, Fields: fields}
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
	if f.Path == "" {
		return FileEvent{}, fmt.Errorf("filemod: %q carries no %s", e.Kind, FieldPath)
	}
	return f, nil
}
