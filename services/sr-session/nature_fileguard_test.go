package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/guardrail"
)

// These cover the file-guard's MATCH SELECTION — how a guard's `match`
// (glob/expression over FileMatchScope) selects a file event — and the FileEvent
// helpers. The check-running is the Runner's (unit-tested there); what
// these pin is that a guard is asked about the right files, reading path, markers
// and context off the file's state.

// postCreate is a settled create event carrying a path and scanned markers.
func postCreate(path string, markers []any) event.Event {
	return event.Event{Kind: declaration.KindPostFileCreate, Fields: map[string]any{
		filemod.FieldPath:       path,
		filemod.FieldNewMarkers: markers,
	}}
}

// marker is a wire-form marker as filemod puts on an event.
func marker(kind, fqn string, line int) any {
	return map[string]any{"kind": kind, "fqn": fqn, "line": line}
}

// A bare-glob match selects files by path and nothing else.
func TestFileGuardSelects_Glob(t *testing.T) {
	m, err := guardrail.CompileFileMatch("memories/**/*.md")
	require.NoError(t, err)

	ok, err := fileGuardSelects(m, postCreate("memories/decisions/x.md", nil))
	require.NoError(t, err)
	assert.True(t, ok, "a path under the glob is selected")

	ok, err = fileGuardSelects(m, postCreate("src/main.go", nil))
	require.NoError(t, err)
	assert.False(t, ok, "a path outside the glob is not selected")
}

// A marker expression selects files by the markers they carry — off the event's
// newMarkers (the settled file's markers on a Post).
func TestFileGuardSelects_Marker(t *testing.T) {
	m, err := guardrail.CompileFileMatch(`any(markers, .kind == "invariant")`)
	require.NoError(t, err)

	withMarker := postCreate("src/x.go", []any{marker("invariant", "User.id", 4)})
	ok, err := fileGuardSelects(m, withMarker)
	require.NoError(t, err)
	assert.True(t, ok, "a file carrying the marker is selected")

	without := postCreate("src/y.go", []any{marker("docs", "User", 1)})
	ok, err = fileGuardSelects(m, without)
	require.NoError(t, err)
	assert.False(t, ok, "a file without the marker is not selected")
}

// A file-guard match that COMPILES but cannot be EVALUATED against the file
// surfaces the error rather than answering false — the fail-closed seam behind
// tests/e2e/session/027 (post_matcher_error), pinned at the dispatch level.
//
// The distinction is the whole point: a match returning (false, nil) says "this
// file does not concern me"; a match returning (_, err) says the engine could not
// DECIDE. fileGuardSelects must not flatten the second into the first — its caller
// (runFileGuardsPost) turns a non-nil error into a
// REFUSAL, because a guard that could not decide must not be read as approval. The
// e2e proves the refusal end to end through the Stop channel; this proves the
// error is produced (not swallowed) at the seam, cheaply and without the mock.
//
// `int(path) > 0` is the shape that reaches the evaluation branch on the flat file
// scope: `int` of a string COMPILES (the checker accepts the conversion), and the
// vm then errors converting "notes.md" at run time. Every well-typed shape
// (`path endsWith ".md"`) answers cleanly instead — this is the one narrow edge,
// the same expression 027 rides.
func TestFileGuardSelects_UnevaluableMatchErrorsNotFalse(t *testing.T) {
	m, err := guardrail.CompileFileMatch("int(path) > 0")
	require.NoError(t, err, "int(path) must COMPILE — the eval-error branch is only reachable past a clean compile")

	_, err = fileGuardSelects(m, postCreate("notes.md", nil))
	require.Error(t, err,
		"a match that cannot be evaluated must surface the error (fail-closed), not answer false — false would read as 'this file does not concern me'")
}

// fileMatchScopeEvent builds a FLAT scope — path and markers at the top
// level, not the event nested under `event` a gate reads.
func TestFileMatchScopeEvent_Flat(t *testing.T) {
	e := postCreate("src/x.go", []any{marker("docs", "X", 2)})
	scope := fileMatchScopeEvent(e)

	assert.Equal(t, "src/x.go", scope.Fields["path"])
	markers, ok := scope.Fields["markers"].([]any)
	require.True(t, ok)
	assert.Len(t, markers, 1)
	assert.NotContains(t, scope.Fields, "context", "a file-guard cannot see session state")
}

// A delete event carries no newMarkers, and one carrying no oldMarkers either
// leaves fileMarkers nothing to read; it yields an empty (non-nil) list so
// `any(markers, …)` evaluates false rather than erroring. (A delete that DOES
// carry oldMarkers reads them — TestFileMarkers_DeleteReadsOldMarkers.)
func TestFileMarkers_DeleteHasEmptyList(t *testing.T) {
	del := event.Event{Kind: declaration.KindPostFileDelete, Fields: map[string]any{filemod.FieldPath: "gone.md"}}
	markers := fileMarkers(del)
	assert.NotNil(t, markers)
	assert.Empty(t, markers)

	delWithEmpty := event.Event{Kind: declaration.KindPostFileDelete, Fields: map[string]any{
		filemod.FieldPath:       "gone.md",
		filemod.FieldOldMarkers: []any{},
	}}
	markers = fileMarkers(delWithEmpty)
	assert.NotNil(t, markers)
	assert.Empty(t, markers)
}

// A create/update event whose newMarkers is genuinely empty (all markers
// stripped, or never had any) must NOT fall back to oldMarkers — the fallback is
// keyed on newMarkers being ABSENT (delete kinds), not merely empty, or a create/
// update that legitimately dropped its last marker would wrongly keep matching
// on what the file used to carry.
func TestFileMarkers_UpdateWithEmptyNewMarkersDoesNotFallBack(t *testing.T) {
	upd := event.Event{Kind: declaration.KindPostFileUpdate, Fields: map[string]any{
		filemod.FieldPath:       "x.go",
		filemod.FieldOldMarkers: []any{marker("invariant", "User.id", 4)},
		filemod.FieldNewMarkers: []any{},
	}}
	markers := fileMarkers(upd)
	assert.NotNil(t, markers)
	assert.Empty(t, markers, "an update that stripped its last marker must read as marker-less, not fall back to the stale oldMarkers")
}

// resultKnown reads the PreFileUpdate resultKnown flag: true only when the write's
// outcome was computable, which is what a pre-write gate requires to verify.
func TestResultKnown(t *testing.T) {
	known := event.Event{Kind: declaration.KindPreFileUpdate, Fields: map[string]any{
		filemod.FieldPath: "x.md", filemod.FieldResultKnown: true,
	}}
	assert.True(t, resultKnown(known))

	unknown := event.Event{Kind: declaration.KindPreFileUpdate, Fields: map[string]any{
		filemod.FieldPath: "x.md", filemod.FieldResultKnown: false,
	}}
	assert.False(t, resultKnown(unknown), "an uncomputed result reads as not-known")

	absent := event.Event{Kind: declaration.KindPreFileUpdate, Fields: map[string]any{filemod.FieldPath: "x.md"}}
	assert.False(t, resultKnown(absent), "an absent resultKnown reads as not-known")
}

// isUnderivablePreWrite fires only on a create or update whose result is unknown,
// and never on a delete (which has no result to be unknown about).
func TestIsUnderivablePreWrite(t *testing.T) {
	mk := func(kind string, fields map[string]any) event.Event {
		return event.Event{Kind: kind, Fields: fields}
	}
	// Create/update with resultKnown false → underivable.
	assert.True(t, isUnderivablePreWrite(mk(declaration.KindPreFileCreate, map[string]any{filemod.FieldResultKnown: false})))
	assert.True(t, isUnderivablePreWrite(mk(declaration.KindPreFileUpdate, map[string]any{filemod.FieldResultKnown: false})))
	// resultKnown absent also reads as underivable (fail-closed).
	assert.True(t, isUnderivablePreWrite(mk(declaration.KindPreFileCreate, map[string]any{})))
	// resultKnown true → derivable, not underivable.
	assert.False(t, isUnderivablePreWrite(mk(declaration.KindPreFileCreate, map[string]any{filemod.FieldResultKnown: true})))
	assert.False(t, isUnderivablePreWrite(mk(declaration.KindPreFileUpdate, map[string]any{filemod.FieldResultKnown: true})))
	// A delete has no result — never underivable, even though it carries no
	// resultKnown field.
	assert.False(t, isUnderivablePreWrite(mk(declaration.KindPreFileDelete, map[string]any{})),
		"a delete carries no result and must not be treated as an unverifiable write")
}

// writeExecutable writes an executable script into dir for a test check.
func writeExecutable(t *testing.T, dir, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755))
}
