package main

import (
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// These cover the new-format dispatch's own wiring logic — the parts between the
// hook payload and the shared check-runner: how a gate trigger's `event` (alias
// included) matches a fired event, which file-write paths the structure gate is
// asked about, the bound-kinds computation that drives extraction, and the gates[]
// map persistence a context reads next slice. The end-to-end path is the e2e
// suite; these pin the seams that suite drives through.

func discard() *cobra.Command {
	c := &cobra.Command{}
	c.SetOut(nopWriter{})
	c.SetErr(nopWriter{})
	return c
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

// natureBoundKinds expands a gate's PreFileWrite alias to the concrete pair, so the
// modules that produce those kinds are asked. An unexpanded alias would name a kind
// no module emits, and no events would be extracted.
func TestNatureBoundKinds_ExpandsAlias(t *testing.T) {
	loaded := declaration.Loaded{
		Gates: []declaration.Gate{{
			Name: "g",
			On:   []declaration.GateTrigger{{Event: declaration.AliasPreFileWrite}},
		}},
	}
	bound := natureBoundKinds(loaded)
	assert.Contains(t, bound, declaration.KindPreFileCreate)
	assert.Contains(t, bound, declaration.KindPreFileUpdate)
	assert.NotContains(t, bound, declaration.AliasPreFileWrite, "the alias itself is not a kind any module emits")
}

// The structure gate binds the two file-write kinds so its paths are extracted even
// when no gate names them.
func TestNatureBoundKinds_StructureBindsWriteKinds(t *testing.T) {
	loaded := declaration.Loaded{Structure: &declaration.StructureGate{}}
	bound := natureBoundKinds(loaded)
	assert.Contains(t, bound, declaration.KindPreFileCreate)
	assert.Contains(t, bound, declaration.KindPreFileUpdate)
}

// firstMatchingEvent wakes a gate when a fired event's kind is one its trigger
// expands to AND the trigger's match holds — and reports no match otherwise.
func TestFirstMatchingEvent(t *testing.T) {
	reg, err := modules.Registry()
	require.NoError(t, err)

	// A gate on PreFileWrite narrowed to memories/.
	g := declaration.Gate{
		Name: "g",
		On: []declaration.GateTrigger{{
			Event: declaration.AliasPreFileWrite,
			Match: `event.path startsWith "memories/"`,
		}},
	}

	// A create under memories/ matches (the alias covers create).
	match := event.Event{Kind: declaration.KindPreFileCreate, Fields: map[string]any{"path": "memories/a.md"}}
	fired, ok := firstMatchingEvent(discard(), reg, g, []event.Event{match})
	assert.True(t, ok, "a create under memories/ wakes a PreFileWrite gate narrowed to memories/")
	assert.Equal(t, match.Kind, fired.Kind)

	// A create OUTSIDE memories/ does not match (the trigger's match narrows it).
	outside := event.Event{Kind: declaration.KindPreFileCreate, Fields: map[string]any{"path": "src/a.go"}}
	_, ok = firstMatchingEvent(discard(), reg, g, []event.Event{outside})
	assert.False(t, ok, "a write outside the match does not wake the gate")

	// A Stop event does not match a PreFileWrite gate at all (wrong kind).
	stop := event.Event{Kind: declaration.KindStop, Fields: map[string]any{}}
	_, ok = firstMatchingEvent(discard(), reg, g, []event.Event{stop})
	assert.False(t, ok, "a Stop does not wake a PreFileWrite gate")
}

// A gate with no match on its trigger wakes on every occurrence of the kind.
func TestFirstMatchingEvent_NoMatchWakesAlways(t *testing.T) {
	reg, err := modules.Registry()
	require.NoError(t, err)

	g := declaration.Gate{Name: "g", On: []declaration.GateTrigger{{Event: declaration.KindStop}}}
	stop := event.Event{Kind: declaration.KindStop, Fields: map[string]any{}}
	_, ok := firstMatchingEvent(discard(), reg, g, []event.Event{stop})
	assert.True(t, ok, "a Stop gate with no match wakes on a Stop")
}

// writePath returns the path for a create/update and nothing for a delete — the
// structure gate governs writes, and a delete is not a write.
func TestWritePath(t *testing.T) {
	create := event.Event{Kind: declaration.KindPreFileCreate, Fields: map[string]any{"path": "a.md"}}
	p, ok := writePath(create)
	assert.True(t, ok)
	assert.Equal(t, "a.md", p)

	update := event.Event{Kind: declaration.KindPreFileUpdate, Fields: map[string]any{"path": "b.md"}}
	p, ok = writePath(update)
	assert.True(t, ok)
	assert.Equal(t, "b.md", p)

	del := event.Event{Kind: declaration.KindPreFileDelete, Fields: map[string]any{"path": "c.md"}}
	_, ok = writePath(del)
	assert.False(t, ok, "a delete is not a write the structure gate governs")
}

// The gates[] map round-trips through the store: a verdict recorded under a gate's
// name reads back as that gate's status.
func TestGatesMapPersistence(t *testing.T) {
	store, err := sessionstate.Open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	cmd := discard()
	gatesMap := map[string]natures.GateState{}

	recordGateVerdict(cmd, store, gatesMap, "checkpoint", natures.GateStatusPass)
	recordGateVerdict(cmd, store, gatesMap, "gatekeeper", natures.GateStatusFail)

	// In-memory map updated for later gates in the same dispatch.
	assert.Equal(t, natures.GateStatusPass, gatesMap["checkpoint"].Status)
	assert.Equal(t, natures.GateStatusFail, gatesMap["gatekeeper"].Status)

	// And persisted: a fresh read of the store returns the same verdicts, which is
	// what the next cycle (and a context, next slice) sees.
	reloaded := loadGatesMap(cmd, store)
	assert.Equal(t, natures.GateStatusPass, reloaded["checkpoint"].Status)
	assert.Equal(t, natures.GateStatusFail, reloaded["gatekeeper"].Status)
}

// loadGatesMap on a store with no gate verdicts is an empty (non-nil) map — a
// gate reading prior verdicts before any ran sees an empty world, not a nil.
func TestLoadGatesMap_Empty(t *testing.T) {
	store, err := sessionstate.Open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	m := loadGatesMap(discard(), store)
	assert.NotNil(t, m)
	assert.Empty(t, m)
}

// gateMatchEvent nests the fired event's fields under `event`, the shape a gate
// matcher reads — proven by a compiled gate match reading event.path through it.
func TestGateMatchEvent_NestsUnderEvent(t *testing.T) {
	e := event.Event{Kind: declaration.KindPreFileCreate, Fields: map[string]any{"path": "memories/a.md"}}
	nested := gateMatchEvent(e)
	inner, ok := nested.Fields["event"].(map[string]any)
	require.True(t, ok, "the fired event's fields are nested under `event`")
	assert.Equal(t, "memories/a.md", inner["path"])
}
