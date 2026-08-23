package natures

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These types are the domain shapes the spec's ContextState and GateState
// describe. The tests pin the shape a `match` expression and the declaration
// loader will both read, so a drift from the spec is caught here rather than in
// whichever slice happens to depend on it next.

func TestContextState_HoldsActiveAndPayload(t *testing.T) {
	s := ContextState{
		Active:  true,
		Payload: map[string]any{"goal": "ship the slice"},
	}

	assert.True(t, s.Active)
	assert.Equal(t, "ship the slice", s.Payload["goal"])
}

// The pair is kept rather than a bare payload because a context's LAST payload
// still matters after it goes inactive — active and payload are independent
// questions.
func TestContextState_InactiveStillCarriesPayload(t *testing.T) {
	s := ContextState{
		Active:  false,
		Payload: map[string]any{"measured": 3},
	}

	assert.False(t, s.Active)
	assert.Equal(t, 3, s.Payload["measured"], "an inactive context keeps its last payload")
}

func TestGateState_HoldsStatus(t *testing.T) {
	assert.Equal(t, GateStatusPass, GateState{Status: GateStatusPass}.Status)
	assert.Equal(t, GateStatusFail, GateState{Status: GateStatusFail}.Status)
}

// The verdict is the spec's minimum, pass | fail, and the string values match
// what a rule reads — `gates[<name>].status == "pass"`.
func TestGateStatus_ValuesMatchTheSpec(t *testing.T) {
	assert.Equal(t, "pass", string(GateStatusPass))
	assert.Equal(t, "fail", string(GateStatusFail))
}

// A round-trip through JSON is the shape these cross a process boundary in — a
// context's state is written by one binary and read by another as `{active,
// payload}`, a gate's as `{status}`. The field spellings are what a matcher's
// `context[<name>].active` and `gates[<name>].status` read, so they are part of
// the contract, not an implementation detail.
func TestState_JSONShape(t *testing.T) {
	cs, err := json.Marshal(ContextState{Active: true, Payload: map[string]any{"k": "v"}})
	require.NoError(t, err)
	var back map[string]any
	require.NoError(t, json.Unmarshal(cs, &back))
	assert.Contains(t, back, "active")
	assert.Contains(t, back, "payload")

	gs, err := json.Marshal(GateState{Status: GateStatusPass})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(gs, &back))
	assert.Contains(t, back, "status")
}
