package declaration

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/natures"
)

// These tests pin the WIRE shape of the payload and judge-input types — the JSON
// a check reads on stdin and the flat variable namespace a judge template renders
// against. The field spellings and the spread-flat structure are the spec's
// contract (dot-dir-file-store/main.tsp), not decoration: a judge template reading
// `{{ event.newContent }}` or `payload.context[<name>]` is coupled to exactly
// these names, so a drift here is a template silently reading nothing.

// FlatEvent serializes an event FLAT: `kind` and every field directly under
// `event`, NOT through the `{kind, fields}` envelope. This is the shape the spec
// models and every example script/template reads (`.event.path`, `.event.newContent`,
// `.event.kind`), and it is the load-bearing property the whole next slice rests on.
func TestFlatEvent_SerializesFieldsFlat(t *testing.T) {
	raw, err := json.Marshal(FlatEvent{
		Kind: "PreFileUpdate",
		Fields: map[string]any{
			"path":       "memories/a.md",
			"newContent": "# hi",
			"newMarkers": []any{map[string]any{"kind": "asked", "fqn": "F"}},
		},
	})
	require.NoError(t, err)

	var back map[string]any
	require.NoError(t, json.Unmarshal(raw, &back))

	// kind and the fields are DIRECTLY under event, side by side.
	assert.Equal(t, "PreFileUpdate", back["kind"])
	assert.Equal(t, "memories/a.md", back["path"], ".event.path must be reachable flat")
	assert.Equal(t, "# hi", back["newContent"], ".event.newContent must be reachable flat")
	assert.Contains(t, back, "newMarkers")
	// There is NO `fields` sub-object — the old nested envelope must not reappear.
	assert.NotContains(t, back, "fields", "the event must be flat, not nested under `fields`")
}

// A field named `kind` cannot shadow the event's own kind — the discriminator is
// written last and wins. (No module declares such a field; this guards the edge.)
func TestFlatEvent_KindWinsOverAFieldNamedKind(t *testing.T) {
	raw, err := json.Marshal(FlatEvent{Kind: "Stop", Fields: map[string]any{"kind": "impostor"}})
	require.NoError(t, err)
	var back map[string]any
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, "Stop", back["kind"], "the event's kind wins over a stray field named kind")
}

// A subjectless event (a Stop, carrying only its kind) marshals to an object with
// just `kind`, never a null — so a script indexing `.event.<anything>` gets a clean
// miss rather than an error.
func TestFlatEvent_SubjectlessIsAnObject(t *testing.T) {
	raw, err := json.Marshal(FlatEvent{Kind: "Stop"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"kind":"Stop"}`, string(raw))
}

// FlatEvent round-trips: marshal then unmarshal recovers {Kind, Fields}, which a Go
// consumer reading a payload back relies on.
func TestFlatEvent_RoundTrips(t *testing.T) {
	orig := FlatEvent{Kind: "PreFileCreate", Fields: map[string]any{"path": "a.md", "newContent": "x"}}
	raw, err := json.Marshal(orig)
	require.NoError(t, err)
	var back FlatEvent
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, "PreFileCreate", back.Kind)
	assert.Equal(t, "a.md", back.Fields["path"])
	assert.Equal(t, "x", back.Fields["newContent"])
}

// A FileJudgeInput spreads CheckPayload's fields flat at the top level — `event`,
// `transcriptPath` render unprefixed — and carries additionalContext
// as one more top-level field, present only when set. AND `event` itself is flat,
// so `{{ event.newContent }}` reaches the content.
func TestFileJudgeInput_SpreadsPayloadFlat(t *testing.T) {
	in := FileJudgeInput{
		CheckPayload: CheckPayload{
			Event:          FlatEvent{Kind: "PostFileUpdate", Fields: map[string]any{"path": "a.md", "newContent": "body"}},
			TranscriptPath: "/t.jsonl",
		},
		AdditionalContext: PreparedContext{"doc_text": "hello"},
	}
	raw, err := json.Marshal(in)
	require.NoError(t, err)

	var back map[string]any
	require.NoError(t, json.Unmarshal(raw, &back))

	// The payload's own fields are at the TOP level, not nested under "payload" or
	// "checkPayload" — this is what makes `{{ event.newContent }}` render.
	assert.Contains(t, back, "event")
	assert.Contains(t, back, "transcriptPath")
	assert.NotContains(t, back, "context")
	assert.Contains(t, back, "additionalContext")
	assert.NotContains(t, back, "CheckPayload", "the payload must spread flat, not nest under its Go type name")
	assert.NotContains(t, back, "payload", "the payload spreads flat, it is not wrapped in a `payload` key")

	// event is flat: newContent directly under event, no `fields` sub-object.
	ev := back["event"].(map[string]any)
	assert.Equal(t, "body", ev["newContent"], "{{ event.newContent }} must resolve — the event is flat")
	assert.NotContains(t, ev, "fields", "the judge's event must not be the nested envelope")
}

// additionalContext is OMITTED when no prepare ran — so a judge template can test
// its presence, and its absence is a clean miss rather than a null.
func TestFileJudgeInput_OmitsAdditionalContextWhenAbsent(t *testing.T) {
	in := FileJudgeInput{
		CheckPayload: CheckPayload{
			Event:          FlatEvent{Kind: "PostFileCreate", Fields: map[string]any{}},
			TranscriptPath: "/t.jsonl",
		},
	}
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	var back map[string]any
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.NotContains(t, back, "additionalContext", "additionalContext is present only when prepare returned one")
}

// A GateJudgeInput spreads GateCheckPayload the same way, and its event is flat too.
func TestGateJudgeInput_SpreadsPayloadFlat(t *testing.T) {
	in := GateJudgeInput{
		GateCheckPayload: GateCheckPayload{
			Event:          FlatEvent{Kind: "PreCommandInvoke", Fields: map[string]any{"invocations": []any{map[string]any{"bin": "npm"}}}},
			TranscriptPath: "/t.jsonl",
			Context:        map[string]natures.ContextState{"goal-tracking": {Active: true, Payload: map[string]any{"target": 0.95}}},
		},
	}
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	var back map[string]any
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Contains(t, back, "event")
	assert.Contains(t, back, "transcriptPath")
	assert.Contains(t, back, "context")
	assert.NotContains(t, back, "additionalContext")

	// event.invocations is reachable flat.
	ev := back["event"].(map[string]any)
	assert.Contains(t, ev, "invocations", "{{ event.invocations }} must resolve flat")
	assert.NotContains(t, ev, "fields")
}

// A file-guard's check payload carries no `context`: it judges committed bytes
// and cannot see session state.
func TestCheckPayload_CarriesNoContext(t *testing.T) {
	raw, err := json.Marshal(CheckPayload{Event: FlatEvent{Kind: "PostFileUpdate"}, TranscriptPath: "/t"})
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "context")
}

// A context's enter and exit payloads carry `gates` — how a context reads a
// paired gate's verdict back. The gate state is internal/natures' GateState. And
// the event they carry is flat, so a context script reads `.event.newContent` /
// `.event.path` / `.event.kind` directly.
func TestContextPayloads_CarryGates(t *testing.T) {
	enter := ContextEnterPayload{
		Event:          FlatEvent{Kind: "PostFileUpdate", Fields: map[string]any{"path": "goal/x/goal.yaml", "newContent": "enabled: true"}},
		TranscriptPath: "/t",
		Gates:          map[string]natures.GateState{"goal-verify": {Status: natures.GateStatusFail}},
		CurrentContext: natures.ContextState{Active: true},
	}
	raw, err := json.Marshal(enter)
	require.NoError(t, err)
	var back map[string]any
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Contains(t, back, "gates")
	assert.Contains(t, back, "currentContext")
	// A context's enter script reads .event.newContent / .event.path flat.
	ev := back["event"].(map[string]any)
	assert.Equal(t, "enabled: true", ev["newContent"], "a context enter script reads .event.newContent flat")
	assert.Equal(t, "goal/x/goal.yaml", ev["path"])
	assert.NotContains(t, ev, "fields")

	exit := ContextExitPayload{
		Event:          FlatEvent{Kind: "Stop"},
		TranscriptPath: "/t",
		CurrentContext: natures.ContextState{Active: true},
		Gates:          map[string]natures.GateState{"goal-verify": {Status: natures.GateStatusPass}},
	}
	raw, err = json.Marshal(exit)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Contains(t, back, "gates")
	assert.Contains(t, back, "currentContext")
	exitEv := back["event"].(map[string]any)
	assert.Equal(t, "Stop", exitEv["kind"], "a context exit reads .event.kind flat")
}

// The check payloads do NOT carry `gates` — the spec gives neither a file-guard's
// nor a gate's check a gates map (a gate depends upstream through `require`, not
// this field). Pinned so adding one is a deliberate spec change, not a drift.
func TestCheckPayloads_HaveNoGates(t *testing.T) {
	fileRaw, err := json.Marshal(CheckPayload{Event: FlatEvent{Kind: "PostFileCreate"}})
	require.NoError(t, err)
	var back map[string]any
	require.NoError(t, json.Unmarshal(fileRaw, &back))
	assert.NotContains(t, back, "gates", "a file-guard's CheckPayload carries no gates")

	gateRaw, err := json.Marshal(GateCheckPayload{Event: FlatEvent{Kind: "Stop"}})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(gateRaw, &back))
	assert.NotContains(t, back, "gates", "a gate's GateCheckPayload carries no gates")
}

// The Event() accessor returns the envelope form back, for a Go consumer that wants
// event.Event after reading a payload. (Keeps the event import used and the
// conversion honest.)
func TestFlatEvent_EventAccessor(t *testing.T) {
	fe := FlatEvent{Kind: "PreFileCreate", Fields: map[string]any{"path": "a.md"}}
	var e event.Event = fe.Event()
	assert.Equal(t, "PreFileCreate", e.Kind)
	assert.Equal(t, "a.md", e.Fields["path"])
}
