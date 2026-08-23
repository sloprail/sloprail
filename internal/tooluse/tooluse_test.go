package tooluse

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
)

// pending is a test double for a tool call a harness is about to make.
type pending struct {
	tool string
	args json.RawMessage
}

func (p pending) Tool() string               { return p.tool }
func (p pending) Arguments() json.RawMessage { return p.args }

func preInput(p Pending) module.Input {
	return module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: p,
	}
}

func TestModule_Name(t *testing.T) {
	assert.Equal(t, Name, New().Name())
	assert.Equal(t, "tooluse", Name, "the module name is not a prefix the kinds carry")
}

func TestModule_DeclaresPreToolUseWithToolAndInput(t *testing.T) {
	kinds := New().Kinds()
	require.Len(t, kinds, 1)
	assert.Equal(t, KindPreToolUse, kinds[0].Name)

	byName := map[string]module.FieldType{}
	for _, f := range kinds[0].Fields {
		byName[f.Name] = f.Type
	}
	assert.Equal(t, map[string]module.FieldType{
		FieldTool:  module.TypeString,
		FieldInput: module.TypeMap,
	}, byName, "PreToolUse carries the tool name and its input")
}

// TestModule_InputIsAnOpenMap: `input` must be a map with NO enumerated keys, so
// a rule reading `input.file_path` reaches into it unchecked. A closed type
// would refuse a key the engine has not heard of — the checker punishing an
// author for a tool vocabulary this module deliberately does not own.
func TestModule_InputIsAnOpenMap(t *testing.T) {
	for _, f := range New().Kinds()[0].Fields {
		if f.Name == FieldInput {
			assert.Equal(t, module.TypeMap, f.Type)
			assert.Empty(t, f.Fields, "input must not enumerate keys — it is Record<unknown>")
		}
	}
}

func TestExtract_OneEventCarryingToolAndInput(t *testing.T) {
	events, err := New().Extract(preInput(pending{
		tool: "WebFetch",
		args: json.RawMessage(`{"url":"https://example.com","depth":2}`),
	}))
	require.NoError(t, err)
	require.Len(t, events, 1, "one tool call produces exactly one PreToolUse")

	e := events[0]
	assert.Equal(t, KindPreToolUse, e.Kind)
	assert.Equal(t, "WebFetch", e.Fields[FieldTool])
	assert.Equal(t, map[string]any{
		"url":   "https://example.com",
		"depth": float64(2), // JSON numbers decode to float64
	}, e.Fields[FieldInput], "the input is carried as the harness reported it")
}

// TestExtract_FiresForAnyTool: the module does NOT branch on the shape of the
// arguments the way the file and command modules do. A tool whose arguments name
// no file and no command — an MCP call, a web fetch — still produces a
// PreToolUse, because the question this kind answers is "what tool is this",
// not "what does it do".
func TestExtract_FiresForAnyTool(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    pending
	}{
		{"web fetch", pending{tool: "WebFetch", args: json.RawMessage(`{"url":"x"}`)}},
		{"mcp call", pending{tool: "mcp__server__do", args: json.RawMessage(`{"a":1}`)}},
		{"read", pending{tool: "Read", args: json.RawMessage(`{"file_path":"/a"}`)}},
		{"bash", pending{tool: "Bash", args: json.RawMessage(`{"command":"ls"}`)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events, err := New().Extract(preInput(tc.p))
			require.NoError(t, err)
			require.Len(t, events, 1)
			assert.Equal(t, tc.p.tool, events[0].Fields[FieldTool])
		})
	}
}

func TestExtract_EmptyInputWhenNoArguments(t *testing.T) {
	// A tool called with nothing still gets an event, with input as an empty
	// object — the fact worth reporting is that the tool is about to run, and a
	// rule narrowing on `tool` alone must see it.
	events, err := New().Extract(preInput(pending{tool: "NoArgsTool", args: nil}))
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "NoArgsTool", events[0].Fields[FieldTool])
	assert.Equal(t, map[string]any{}, events[0].Fields[FieldInput],
		"absent arguments become an empty object, never null")
}

func TestExtract_NonObjectArgumentsStillProduceAnEvent(t *testing.T) {
	// Arguments that are valid JSON but not an object (a bare string, a number, a
	// list) leave input empty rather than dropping the event. The tool is still
	// about to run.
	for _, tc := range []struct {
		name string
		args string
	}{
		{"string", `"just a string"`},
		{"number", `42`},
		{"list", `["a","b"]`},
		{"malformed", `{not json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events, err := New().Extract(preInput(pending{tool: "T", args: json.RawMessage(tc.args)}))
			require.NoError(t, err)
			require.Len(t, events, 1, "the event exists regardless of the input's shape")
			assert.Equal(t, map[string]any{}, events[0].Fields[FieldInput])
		})
	}
}

func TestExtract_NothingInThePostPhase(t *testing.T) {
	// A tool having run is not a difference a diff establishes; the file module's
	// Post events carry what it changed. This module produces nothing after the
	// fact.
	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePost,
		module.InputPayload: pending{tool: "WebFetch", args: json.RawMessage(`{"url":"x"}`)},
	})
	require.NoError(t, err)
	assert.Empty(t, events, "no Post tool-use event")
}

func TestExtract_NotAPendingPayloadProducesNothing(t *testing.T) {
	// A payload this module does not recognise concerns it not at all, which is
	// ordinary rather than an error.
	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: "not a pending",
	})
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestEvent_InputIsAnObjectOnTheWire(t *testing.T) {
	// A nil input must serialise as {} rather than null, so a hook doing the
	// obvious `.input.whatever` gets a clean miss rather than an error.
	e := ToolEvent{Tool: "T"}.Event()
	assert.Equal(t, map[string]any{}, e.Fields[FieldInput])
}

func TestFromEvent_RoundTrip(t *testing.T) {
	in := ToolEvent{Tool: "WebFetch", Input: map[string]any{"url": "x", "n": float64(3)}}
	got, err := FromEvent(in.Event())
	require.NoError(t, err)
	assert.Equal(t, in, got)
}

func TestFromEvent_EmptyInputRoundTripsToEmptyMap(t *testing.T) {
	got, err := FromEvent(ToolEvent{Tool: "T"}.Event())
	require.NoError(t, err)
	assert.Equal(t, "T", got.Tool)
	assert.Equal(t, map[string]any{}, got.Input, "the wire form is an empty object, not nil")
}

func TestFromEvent_NoToolIsAnError(t *testing.T) {
	// Silently returning a zero value would let a caller act on a tool that was
	// never named.
	for name, e := range map[string]event.Event{
		"nil fields":   {Kind: KindPreToolUse},
		"empty fields": {Kind: KindPreToolUse, Fields: map[string]any{}},
		"empty tool":   {Kind: KindPreToolUse, Fields: map[string]any{FieldTool: ""}},
		"input only":   {Kind: KindPreToolUse, Fields: map[string]any{FieldInput: map[string]any{"a": 1}}},
		"non-string":   {Kind: KindPreToolUse, Fields: map[string]any{FieldTool: 42}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := FromEvent(e)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "carries no "+FieldTool)
		})
	}
}
