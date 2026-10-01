package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
)

// These pin what a file-guard's `deletions:` value and its markers do to how the
// guard is bound. (How `deletions:` filters a changeset is pinned in
// internal/changeset, and end to end in tests/e2e/changeset.)

// deletionModes is every value a guard's `deletions:` can take, the absent key
// (the default) included.
var deletionModes = []declaration.Deletions{"", declaration.DeletionsSkip, declaration.DeletionsInclude, declaration.DeletionsOnly}

// fileMarkers reads oldMarkers on a delete, and newMarkers everywhere else — an
// update whose result dropped every marker reads as marker-less, never as the
// markers it just removed.
func TestFileMarkers_DeleteReadsOldMarkers(t *testing.T) {
	was := []any{marker("invariant", "x", 1)}
	for _, kind := range []string{declaration.KindPreFileDelete, declaration.KindPostFileDelete} {
		del := event.Event{Kind: kind, Fields: map[string]any{filemod.FieldOldMarkers: was}}
		assert.Equal(t, was, fileMarkers(del), kind)
	}
	upd := event.Event{Kind: declaration.KindPostFileUpdate, Fields: map[string]any{
		filemod.FieldOldMarkers: was,
		filemod.FieldNewMarkers: []any{},
	}}
	assert.Empty(t, fileMarkers(upd), "an update that stripped its markers is marker-less")
	assert.NotNil(t, fileMarkers(upd))
}

// A file-guard binds nothing at pre-tool, whatever its `deletions:`: it judges the
// settled file at Stop, and only a gate acts before a write.
func TestNaturePreToolBoundKinds_FileGuardsBindNothing(t *testing.T) {
	for _, mode := range deletionModes {
		loaded := declaration.Loaded{FileGuards: []declaration.FileGuard{{Name: "g", Deletions: mode}}}
		assert.Empty(t, naturePreToolBoundKinds(loaded), "deletions=%q", mode)
	}
}
