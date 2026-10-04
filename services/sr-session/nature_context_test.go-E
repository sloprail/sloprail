package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/grounding"
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// These cover the context[] map's persistence and the trigger-matching seam the
// context lifecycle drives — the parts between the hook and dispatch-core's
// EnterContext/ExitContext (whose own behaviour is unit-tested in
// internal/dispatch). The end-to-end enter/exit path is the e2e suite.

// loadContextMap seeds EVERY declared context inactive, so a never-entered
// context is present-and-false rather than absent — which is what makes
// `not context["x"].active` usable.
func TestLoadContextMap_SeedsInactive(t *testing.T) {
	store, err := sessionstate.Open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	contexts := []declaration.Context{{Name: "refactor"}, {Name: "research-run"}}
	m := loadContextMap(discard(), store, contexts)

	require.Contains(t, m, "refactor")
	require.Contains(t, m, "research-run")
	assert.False(t, m["refactor"].Active, "a never-entered context is present and inactive")
	assert.NotNil(t, m["refactor"].Payload, "payload is a non-nil object, so indexing it is a clean miss")
}

// The context[] map round-trips through the store: a recorded state reads back
// under the same name, which is what a later cycle, a gate, and a file-guard see.
func TestContextStatePersistence(t *testing.T) {
	store, err := sessionstate.Open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	cmd := discard()
	m := map[string]natures.ContextState{}

	recordContextState(cmd, store, m, "refactor", natures.ContextState{Active: true, Payload: map[string]any{"scope": "src/"}})

	// In-memory map updated for later readers in the same dispatch.
	assert.True(t, m["refactor"].Active)
	assert.Equal(t, "src/", m["refactor"].Payload["scope"])

	// And persisted: a fresh read (seeded with the declaration) returns it.
	reloaded := loadContextMap(cmd, store, []declaration.Context{{Name: "refactor"}})
	assert.True(t, reloaded["refactor"].Active)
	assert.Equal(t, "src/", reloaded["refactor"].Payload["scope"])
}

// A context marked inactive KEEPS its last payload — a later cycle can read what a
// closed loop last measured (spec ContextState: payload survives inactivity).
func TestContextStatePersistence_InactiveKeepsPayload(t *testing.T) {
	store, err := sessionstate.Open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	cmd := discard()
	m := map[string]natures.ContextState{}
	recordContextState(cmd, store, m, "eval-loop", natures.ContextState{Active: false, Payload: map[string]any{"iters": 7}})

	reloaded := loadContextMap(cmd, store, []declaration.Context{{Name: "eval-loop"}})
	assert.False(t, reloaded["eval-loop"].Active)
	assert.Equal(t, float64(7), reloaded["eval-loop"].Payload["iters"], "an inactive context still carries its last payload")
}

// contextMatchValue produces the wire form an expression indexes — lowercase
// active/payload — for every context, so a matcher reading context[<name>] works.
func TestContextMatchValue_WireForm(t *testing.T) {
	m := map[string]natures.ContextState{
		"a": {Active: true, Payload: map[string]any{"k": "v"}},
		"b": {Active: false, Payload: nil},
	}
	wire := contextMatchValue(m)

	a, ok := wire["a"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, a["active"])
	ap, ok := a["payload"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "v", ap["k"])

	// A nil payload becomes a non-nil object, so `.payload.x` is a clean miss.
	b, ok := wire["b"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, b["active"])
	assert.NotNil(t, b["payload"])
}

// contextMatchingEvents wakes a context on EVERY matching occurrence, not just the
// first — the reason it returns a slice where a gate's firstMatchingEvent returns
// one.
func TestContextMatchingEvents_EveryOccurrence(t *testing.T) {
	reg, err := modules.Registry()
	require.NoError(t, err)

	c := declaration.Context{
		Name: "touched",
		On:   []declaration.ContextTrigger{{Event: declaration.AliasPostFileWrite}},
	}
	// Two post-writes in one cycle → enter fires on both.
	events := []event.Event{
		{Kind: declaration.KindPostFileCreate, Fields: map[string]any{"path": "a.md"}},
		{Kind: declaration.KindPostFileUpdate, Fields: map[string]any{"path": "b.md"}},
	}
	matched := contextMatchingEvents(discard(), reg, c, events, nil)
	assert.Len(t, matched, 2, "enter fires on every matching occurrence")
}

// A context trigger's `match` narrows which occurrences wake it, reading the
// event's own fields.
func TestContextMatchingEvents_TriggerMatchNarrows(t *testing.T) {
	reg, err := modules.Registry()
	require.NoError(t, err)

	c := declaration.Context{
		Name: "goal-tracking",
		On: []declaration.ContextTrigger{{
			Event: declaration.KindPostFileCreate,
			Match: `event.path == "goal.yaml"`,
		}},
	}
	events := []event.Event{
		{Kind: declaration.KindPostFileCreate, Fields: map[string]any{"path": "goal.yaml"}},
		{Kind: declaration.KindPostFileCreate, Fields: map[string]any{"path": "other.md"}},
	}
	matched := contextMatchingEvents(discard(), reg, c, events, nil)
	require.Len(t, matched, 1, "only the occurrence the trigger's match admits wakes enter")
	assert.Equal(t, "goal.yaml", matched[0].Fields["path"])
}

// A context's citation prerequisite on a Post event reads the file's history,
// as a file-guard's does: a citation grounds only the change it rode on, so a
// file whose change holds an uncited part does not meet it, and the context
// does not enter.
func TestContextCitationRequireReadsTheHistory(t *testing.T) {
	reg, err := modules.Registry()
	require.NoError(t, err)
	dir := t.TempDir()
	marker := filepath.Join(dir, "entered")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "enter.sh"),
		[]byte("#!/bin/sh\ncat >/dev/null\ntouch '"+marker+"'\necho '{\"active\": true}'\n"), 0o755))
	c := declaration.Context{
		Name:    "cited",
		Dir:     dir,
		On:      []declaration.ContextTrigger{{Event: declaration.AliasPostFileWrite}},
		Require: []declaration.Prerequisite{{Citation: &declaration.CitationPrerequisite{SourceTypes: []string{"user"}}}},
		Enter:   "./enter.sh",
		Exit:    "./enter.sh",
	}
	store := openTestStore(t)
	ev := citedPostEvent(filemod.KindPostUpdate, "a.md", "base", "cited, then more")
	ev.Fields[grounding.FieldCitations] = grounding.ToWire(userCite)
	cited := dispatchcore.HistoryState{Exists: true, Hash: "cited"}
	history := map[string]*dispatchcore.FileHistory{"a.md": {
		Baseline: dispatchcore.HistoryState{Exists: true, Hash: "base"},
		Current:  dispatchcore.HistoryState{Exists: true, Hash: "more"},
		Points: []dispatchcore.HistoryPoint{{Pools: []transcript.SourceType{transcript.SourceUser},
			Before: dispatchcore.HistoryState{Exists: true, Hash: "base"}, After: cited, At: 1}},
	}}

	ctxMap := loadContextMap(discard(), store, []declaration.Context{c})
	runContextEnters(discard(), reg, []declaration.Context{c}, []event.Event{ev}, hookScope{}, store, ctxMap, loadGatesMap(discard(), store), history)
	assert.NoFileExists(t, marker, "a change with an uncited part met the context's citation requirement")

	runContextEnters(discard(), reg, []declaration.Context{c}, []event.Event{ev}, hookScope{}, store, ctxMap, loadGatesMap(discard(), store), nil)
	assert.FileExists(t, marker, "the control: without a history the citation on the event meets it")
}
