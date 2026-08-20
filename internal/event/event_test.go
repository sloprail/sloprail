package event

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The envelope's only behaviour is its wire form: an event is written to a
// hook's stdin as JSON, so the tags are the contract a hook script reads
// against. These tests pin that contract rather than the struct.

func TestEvent_JSONTags(t *testing.T) {
	out, err := json.Marshal(Event{
		Kind:   "PreFileCreate",
		Fields: map[string]any{"path": "memories/a.md"},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"kind":"PreFileCreate","fields":{"path":"memories/a.md"}}`, string(out))
}

func TestEvent_RoundTrip(t *testing.T) {
	cases := map[string]Event{
		"file event": {
			Kind:   "PreFileCreate",
			Fields: map[string]any{"path": "a.md", "content": "# Notes\n"},
		},
		"subjectless": {
			Kind:   "Stop",
			Fields: map[string]any{},
		},
		"nested fields": {
			Kind: "PreCommand",
			Fields: map[string]any{
				"raw": "npm publish --access public",
				"invocations": []any{
					map[string]any{"bin": "npm", "flags": []any{"--access", "public"}},
				},
			},
		},
		"unicode": {
			Kind:   "PreFileCreate",
			Fields: map[string]any{"path": "памʼять/файл.md", "content": "Правило ✅\n"},
		},
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := json.Marshal(in)
			require.NoError(t, err)

			var back Event
			require.NoError(t, json.Unmarshal(out, &back))
			assert.Equal(t, in.Kind, back.Kind)
			assert.Equal(t, in.Fields, back.Fields,
				"a hook must read back exactly what was sent")
		})
	}
}

func TestEvent_FieldsIsAlwaysAnObjectOnTheWire(t *testing.T) {
	// A subjectless event used to serialise "fields":null, which a hook
	// indexing .fields cannot read. The key is always present and always an
	// object, so a hook needs no special case for the events carrying nothing.
	for _, e := range []Event{
		{Kind: "Stop"},                           // nil map
		{Kind: "Stop", Fields: map[string]any{}}, // empty map
	} {
		out, err := json.Marshal(e)
		require.NoError(t, err)
		assert.JSONEq(t, `{"kind":"Stop","fields":{}}`, string(out))
		assert.NotContains(t, string(out), "null")
	}
}

func TestEvent_MarshalDoesNotMutateTheEvent(t *testing.T) {
	// MarshalJSON fills in the empty map on its own copy. An event whose
	// Fields turned from nil into {} by being serialised would be a value that
	// changes when it is looked at.
	e := Event{Kind: "Stop"}
	_, err := json.Marshal(e)
	require.NoError(t, err)
	assert.Nil(t, e.Fields, "serialising an event must not change it")
}

func TestEvent_NestedMarshalAlsoGetsAnObject(t *testing.T) {
	// How a hook actually receives one: nested under a payload, which is what
	// session_pre_tool marshals. A method on the value type is reached here
	// only because the event is stored as a value, so this is worth pinning.
	out, err := json.Marshal(map[string]any{"event": Event{Kind: "Stop"}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"event":{"kind":"Stop","fields":{}}}`, string(out))
}

func TestEvent_NullFieldsStillUnmarshals(t *testing.T) {
	// Reading is unchanged: an event recorded before this, or written by hand,
	// still parses. Only what we emit is constrained.
	var back Event
	require.NoError(t, json.Unmarshal([]byte(`{"kind":"Stop","fields":null}`), &back))
	assert.Nil(t, back.Fields)
}

func TestEvent_ZeroValue(t *testing.T) {
	var e Event
	assert.Empty(t, e.Kind)
	assert.Nil(t, e.Fields)
}

func TestEvent_NumbersComeBackAsFloat64(t *testing.T) {
	// The usual any-typed JSON caveat, worth pinning because a matcher reads
	// these values: an int written into Fields returns as a float64.
	out, err := json.Marshal(Event{Kind: "K", Fields: map[string]any{"n": 42}})
	require.NoError(t, err)

	var back Event
	require.NoError(t, json.Unmarshal(out, &back))
	assert.Equal(t, float64(42), back.Fields["n"])
	assert.IsType(t, float64(0), back.Fields["n"])
}

func TestEvent_UnmarshalIgnoresUnknownKeys(t *testing.T) {
	var e Event
	require.NoError(t, json.Unmarshal(
		[]byte(`{"kind":"K","fields":{"a":1},"extra":"ignored"}`), &e))
	assert.Equal(t, "K", e.Kind)
	assert.Equal(t, map[string]any{"a": float64(1)}, e.Fields)
}
