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

// A FileJudgeInput spreads CheckPayload's fields flat at the top level — `event`,
// `transcriptPath`, `context` render unprefixed — and carries additionalContext
// as one more top-level field, present only when set.
func TestFileJudgeInput_SpreadsPayloadFlat(t *testing.T) {
	in := FileJudgeInput{
		CheckPayload: CheckPayload{
			Event:          event.Event{Kind: "PostFileUpdate", Fields: map[string]any{"path": "a.md"}},
			TranscriptPath: "/t.jsonl",
			Context:        map[string]natures.ContextState{"refactoring": {Active: true}},
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
	assert.Contains(t, back, "context")
	assert.Contains(t, back, "additionalContext")
	assert.NotContains(t, back, "CheckPayload", "the payload must spread flat, not nest under its Go type name")
	assert.NotContains(t, back, "payload", "the payload spreads flat, it is not wrapped in a `payload` key")
}

// additionalContext is OMITTED when no prepare ran — so a judge template can test
// its presence, and its absence is a clean miss rather than a null.
func TestFileJudgeInput_OmitsAdditionalContextWhenAbsent(t *testing.T) {
	in := FileJudgeInput{
		CheckPayload: CheckPayload{
			Event:          event.Event{Kind: "PostFileCreate", Fields: map[string]any{}},
			TranscriptPath: "/t.jsonl",
			Context:        map[string]natures.ContextState{},
		},
	}
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	var back map[string]any
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.NotContains(t, back, "additionalContext", "additionalContext is present only when prepare returned one")
}

// A GateJudgeInput spreads GateCheckPayload the same way.
func TestGateJudgeInput_SpreadsPayloadFlat(t *testing.T) {
	in := GateJudgeInput{
		GateCheckPayload: GateCheckPayload{
			Event:          event.Event{Kind: "Stop", Fields: map[string]any{}},
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
}

// The check payloads carry `context` at parity with their match scope — a check
// reads `context[<name>].active` and `.payload` the same way a matcher does,
// through internal/natures' ContextState.
func TestCheckPayload_CarriesContextAtParity(t *testing.T) {
	p := CheckPayload{
		Event:          event.Event{Kind: "PostFileUpdate"},
		TranscriptPath: "/t",
		Context:        map[string]natures.ContextState{"c": {Active: false, Payload: map[string]any{"measured": 3}}},
	}
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	var back struct {
		Context map[string]struct {
			Active  bool           `json:"active"`
			Payload map[string]any `json:"payload"`
		} `json:"context"`
	}
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.False(t, back.Context["c"].Active)
	assert.EqualValues(t, 3, back.Context["c"].Payload["measured"], "an inactive context still carries its last payload")
}

// A context's enter and exit payloads carry `gates` — how a context reads a
// paired gate's verdict back. The gate state is internal/natures' GateState.
func TestContextPayloads_CarryGates(t *testing.T) {
	enter := ContextEnterPayload{
		Event:          event.Event{Kind: "PostFileUpdate"},
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

	exit := ContextExitPayload{
		Event:          event.Event{Kind: "Stop"},
		TranscriptPath: "/t",
		CurrentContext: natures.ContextState{Active: true},
		Gates:          map[string]natures.GateState{"goal-verify": {Status: natures.GateStatusPass}},
	}
	raw, err = json.Marshal(exit)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Contains(t, back, "gates")
	assert.Contains(t, back, "currentContext")
}

// The check payloads do NOT carry `gates` — the spec gives neither a file-guard's
// nor a gate's check a gates map (a gate depends upstream through `require`, not
// this field). Pinned so adding one is a deliberate spec change, not a drift.
func TestCheckPayloads_HaveNoGates(t *testing.T) {
	fileRaw, err := json.Marshal(CheckPayload{Event: event.Event{Kind: "PostFileCreate"}})
	require.NoError(t, err)
	var back map[string]any
	require.NoError(t, json.Unmarshal(fileRaw, &back))
	assert.NotContains(t, back, "gates", "a file-guard's CheckPayload carries no gates")

	gateRaw, err := json.Marshal(GateCheckPayload{Event: event.Event{Kind: "Stop"}})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(gateRaw, &back))
	assert.NotContains(t, back, "gates", "a gate's GateCheckPayload carries no gates")
}
