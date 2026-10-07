package tooluse

import (
	"fmt"

	"github.com/sloprail/sloprail/internal/event"
)

// ToolEvent is what this module's own code passes around.
//
// Typed here, a map at the boundary — the same split the file and command
// modules make, and for the same reason. The engine has no use for a struct
// whose shape it cannot know, and this module has no use for a map when it is
// the one putting the values in.
type ToolEvent struct {
	// Tool is the tool's canonical name (see FieldTool).
	Tool string

	// NativeTool is the name the harness reported; empty means the same as Tool.
	NativeTool string

	// Input is the tool's own arguments, decoded to a map. Nil when the harness
	// named none or they were not an object; on the wire that becomes an empty
	// object, so a matcher reading `input.whatever` gets a clean miss rather than
	// an error.
	Input map[string]any
}

// Event converts to the wire form.
//
// `input` is carried as an empty object when there is nothing, never omitted:
// the kind declares it, and a field that vanished when empty would make
// `input == {}` — a fair question about a tool called with no arguments — error
// rather than hold. Every kind this module declares carries both its fields.
func (t ToolEvent) Event() event.Event {
	input := t.Input
	if input == nil {
		input = map[string]any{}
	}
	native := t.NativeTool
	if native == "" {
		native = t.Tool
	}
	return event.Event{
		Kind: KindPreToolUse,
		Fields: map[string]any{
			FieldTool:       t.Tool,
			FieldNativeTool: native,
			FieldInput:      input,
		},
	}
}

// FromEvent converts back, for this module's own reads of events it produced.
//
// An event carrying no tool name is an error rather than a zero value: every
// event this module emits names the tool it is about, so one without it did not
// come from here, and returning an empty one would let a caller act on a tool
// that was never named. An absent input is not an error — a tool called with no
// arguments is an ordinary thing — and comes back as an empty map.
func FromEvent(e event.Event) (ToolEvent, error) {
	t := ToolEvent{}
	if v, ok := e.Fields[FieldTool].(string); ok {
		t.Tool = v
	}
	if t.Tool == "" {
		return ToolEvent{}, fmt.Errorf("tooluse: %q carries no %s", e.Kind, FieldTool)
	}
	if v, ok := e.Fields[FieldInput].(map[string]any); ok {
		t.Input = v
	} else {
		t.Input = map[string]any{}
	}
	return t, nil
}
