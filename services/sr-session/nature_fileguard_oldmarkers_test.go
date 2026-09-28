package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/natures"
)

// A marker-scoped guard must be able to see a marker LEAVE. `markers` reads an
// update that stripped the file's last marker as marker-less, so the guard never
// selects the write that removed what it guards; `oldMarkers` does.
func TestRunFileGuardsPost_OldMarkersSelectsAMarkerRemoval(t *testing.T) {
	was := []any{marker("invariant", "x", 1)}
	stripped := event.Event{Kind: declaration.KindPostFileUpdate, Fields: map[string]any{
		filemod.FieldPath:       "src/pinned.go",
		filemod.FieldOldContent: "// sr:invariant x\ncode\n",
		filemod.FieldNewContent: "code\n",
		filemod.FieldOldMarkers: was,
		filemod.FieldNewMarkers: []any{},
	}}

	byMarkers, _ := refusingGuard(t, "", false)
	byMarkers.Match = `any(markers, .kind == "invariant")`
	results := runFileGuardsPost(discard(), []declaration.FileGuard{byMarkers}, []event.Event{stripped},
		newRevalidation(t), hookScope{}, t.TempDir(), map[string]natures.ContextState{})
	assert.Empty(t, results, "markers alone does not see the marker that left — the reason oldMarkers exists")

	byOld, ledger := refusingGuard(t, "", false)
	byOld.Match = `any(oldMarkers, .kind == "invariant")`
	results = runFileGuardsPost(discard(), []declaration.FileGuard{byOld}, []event.Event{stripped},
		newRevalidation(t), hookScope{}, t.TempDir(), map[string]natures.ContextState{})
	require.Len(t, results, 1, "oldMarkers selects the update that removed the marker")
	assert.Equal(t, []string{declaration.KindPostFileUpdate}, ledgerLines(t, ledger))
}

// oldMarkers is what the file held before: none on a create, the carried
// markers on an update or a delete, and always a list.
func TestFileOldMarkers(t *testing.T) {
	was := []any{marker("invariant", "x", 1)}
	create := event.Event{Kind: declaration.KindPreFileCreate, Fields: map[string]any{
		filemod.FieldNewMarkers: was,
	}}
	assert.Empty(t, fileOldMarkers(create))
	assert.NotNil(t, fileOldMarkers(create))
	for _, kind := range []string{declaration.KindPreFileUpdate, declaration.KindPostFileUpdate, declaration.KindPreFileDelete} {
		e := event.Event{Kind: kind, Fields: map[string]any{filemod.FieldOldMarkers: was}}
		assert.Equal(t, was, fileOldMarkers(e), kind)
	}
	assert.Equal(t, []any{}, fileMatchScopeEvent(create, nil).Fields["oldMarkers"])
}
