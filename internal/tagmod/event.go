package tagmod

import (
	"fmt"

	"github.com/sloprail/sloprail/internal/event"
)

// Tag is one `#tag`-shaped token found in an agent message.
type Tag struct {
	// Label is the tag's own text, without the leading `#` — `update` for
	// `#update`.
	Label string
}

// TagEvent is what this module's own code passes around.
//
// Typed here, a map at the boundary — the same split the file and command
// modules make. The engine has no use for a struct whose shape it cannot know,
// and this module has no use for a map when it is the one putting the values in.
type TagEvent struct {
	// Tags are every tag found this cycle, in the order they appeared.
	Tags []Tag
}

// Event converts to the wire form.
//
// `tags` is carried as an empty list when there are none, never omitted: the
// kind declares it, and a field that vanished when empty would make `len(tags)
// == 0` — the natural way to ask "did the agent write any tag this cycle" —
// error rather than hold. The same reasoning the file module gives for carrying
// an empty markers list.
func (t TagEvent) Event() event.Event {
	tags := make([]any, 0, len(t.Tags))
	for _, tag := range t.Tags {
		tags = append(tags, map[string]any{KeyTagLabel: tag.Label})
	}
	return event.Event{
		Kind:   KindPostTagWrite,
		Fields: map[string]any{FieldTags: tags},
	}
}

// FromEvent converts back, for this module's own reads of events it produced.
//
// An empty tags list is not an error — a cycle in which the agent wrote no tag
// is an ordinary thing, and the whole point of carrying the list present-and-
// empty is that `len(tags) == 0` is a question a rule may ask. A tag entry that
// is not a map, or whose label is not a string, is skipped rather than failing
// the whole read: this reads events this module itself produced, so a malformed
// entry is not a case a caller acts on differently.
func FromEvent(e event.Event) (TagEvent, error) {
	if e.Kind != KindPostTagWrite {
		return TagEvent{}, fmt.Errorf("tagmod: %q is not a %s", e.Kind, KindPostTagWrite)
	}
	t := TagEvent{}
	list, _ := e.Fields[FieldTags].([]any)
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if label, ok := m[KeyTagLabel].(string); ok {
			t.Tags = append(t.Tags, Tag{Label: label})
		}
	}
	return t, nil
}
