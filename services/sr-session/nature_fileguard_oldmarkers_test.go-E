package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
)

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
	assert.Equal(t, []any{}, fileMatchScopeEvent(create).Fields["oldMarkers"])
}
