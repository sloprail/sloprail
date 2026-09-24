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
	"github.com/sloprail/sloprail/internal/natures"
)

// These cover the file-guard's MATCH SELECTION — how a guard's `match`
// (glob/expression over FileMatchScope) selects a file event — and the FileEvent
// preventive helpers. The check-running is the Runner's (unit-tested there); what
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

	ok, err := fileGuardSelects(m, postCreate("memories/decisions/x.md", nil), nil)
	require.NoError(t, err)
	assert.True(t, ok, "a path under the glob is selected")

	ok, err = fileGuardSelects(m, postCreate("src/main.go", nil), nil)
	require.NoError(t, err)
	assert.False(t, ok, "a path outside the glob is not selected")
}

// A marker expression selects files by the markers they carry — off the event's
// newMarkers (the settled file's markers on a Post).
func TestFileGuardSelects_Marker(t *testing.T) {
	m, err := guardrail.CompileFileMatch(`any(markers, .kind == "invariant")`)
	require.NoError(t, err)

	withMarker := postCreate("src/x.go", []any{marker("invariant", "User.id", 4)})
	ok, err := fileGuardSelects(m, withMarker, nil)
	require.NoError(t, err)
	assert.True(t, ok, "a file carrying the marker is selected")

	without := postCreate("src/y.go", []any{marker("docs", "User", 1)})
	ok, err = fileGuardSelects(m, without, nil)
	require.NoError(t, err)
	assert.False(t, ok, "a file without the marker is not selected")
}

// A context-gated match reads context[<name>] off the threaded context map: the
// guard applies only inside (or, with `not`, outside) a context.
func TestFileGuardSelects_ContextGated(t *testing.T) {
	// The guard applies to src/ files only while the refactor context is active.
	m, err := guardrail.CompileFileMatch(`path startsWith "src/" and context["refactor"].active`)
	require.NoError(t, err)

	e := postCreate("src/x.go", nil)

	// Refactor inactive → not selected.
	inactive := map[string]natures.ContextState{"refactor": {Active: false, Payload: map[string]any{}}}
	ok, err := fileGuardSelects(m, e, inactive)
	require.NoError(t, err)
	assert.False(t, ok, "guard does not apply while the context is inactive")

	// Refactor active → selected.
	active := map[string]natures.ContextState{"refactor": {Active: true, Payload: map[string]any{"scope": "src/"}}}
	ok, err = fileGuardSelects(m, e, active)
	require.NoError(t, err)
	assert.True(t, ok, "guard applies while the context is active")
}

// The `not context[...].active` idiom (matching a context's ABSENCE) works
// against a seeded-inactive context — the reason the context map must carry every
// declared context, not just the active ones.
func TestFileGuardSelects_NotContextActive(t *testing.T) {
	m, err := guardrail.CompileFileMatch(`not context["refactor"].active`)
	require.NoError(t, err)

	e := postCreate("any.md", nil)
	// A seeded-inactive context reads present-and-false, so `not …active` is true.
	inactive := map[string]natures.ContextState{"refactor": {Active: false, Payload: map[string]any{}}}
	ok, err := fileGuardSelects(m, e, inactive)
	require.NoError(t, err)
	assert.True(t, ok, "not-active matches a declared-but-inactive context")

	active := map[string]natures.ContextState{"refactor": {Active: true, Payload: map[string]any{}}}
	ok, err = fileGuardSelects(m, e, active)
	require.NoError(t, err)
	assert.False(t, ok, "not-active does not match while the context is active")
}

// A file-guard match that COMPILES but cannot be EVALUATED against the file
// surfaces the error rather than answering false — the fail-closed seam behind
// tests/e2e/session/027 (post_matcher_error), pinned at the dispatch level.
//
// The distinction is the whole point: a match returning (false, nil) says "this
// file does not concern me"; a match returning (_, err) says the engine could not
// DECIDE. fileGuardSelects must not flatten the second into the first — its caller
// (runFileGuardsPost / runFileGuardsPreventive) turns a non-nil error into a
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

	_, err = fileGuardSelects(m, postCreate("notes.md", nil), nil)
	require.Error(t, err,
		"a match that cannot be evaluated must surface the error (fail-closed), not answer false — false would read as 'this file does not concern me'")
}

// fileMatchScopeEvent builds a FLAT scope — path, markers, context at the top
// level, not the event nested under `event` a gate reads.
func TestFileMatchScopeEvent_Flat(t *testing.T) {
	e := postCreate("src/x.go", []any{marker("docs", "X", 2)})
	ctx := map[string]natures.ContextState{"c": {Active: true, Payload: map[string]any{"k": "v"}}}
	scope := fileMatchScopeEvent(e, ctx)

	assert.Equal(t, "src/x.go", scope.Fields["path"])
	markers, ok := scope.Fields["markers"].([]any)
	require.True(t, ok)
	assert.Len(t, markers, 1)
	// context is wire-form: indexable with lowercase keys.
	cm, ok := scope.Fields["context"].(map[string]any)
	require.True(t, ok)
	entry, ok := cm["c"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, entry["active"])
}

// A delete event carries no newMarkers at all (filemod's KindPreDelete/
// KindPostDelete field lists declare no newMarkers key). fileMarkers falls back
// to oldMarkers — the markers the deleted file WAS carrying — so a marker-based
// match (`any(markers, .kind == "invariant")`) still selects the delete of a
// file that held the marker. Without this fallback every marker-based
// file-guard's match silently evaluates false on every delete, regardless of
// what the file held — see the business-invariants example, whose own README
// says the rule is about "the file's state... it does not matter which event
// last touched the file."
func TestFileMarkers_DeleteFallsBackToOldMarkers(t *testing.T) {
	del := event.Event{Kind: declaration.KindPostFileDelete, Fields: map[string]any{
		filemod.FieldPath:       "gone.md",
		filemod.FieldOldMarkers: []any{marker("invariant", "User.id", 4)},
	}}
	markers := fileMarkers(del)
	require.Len(t, markers, 1)
	assert.Equal(t, "invariant", markers[0].(map[string]any)["kind"])
}

// A delete event that genuinely carried no markers (oldMarkers absent or empty)
// still yields an empty (non-nil) list, so `any(markers, …)` evaluates false
// rather than erroring — the "always a list" discipline holds either way.
func TestFileMarkers_DeleteWithNoMarkersIsEmptyList(t *testing.T) {
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
// outcome was computable, which is what a preventive guard requires to verify.
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
	assert.False(t, resultKnown(absent), "an absent resultKnown reads as not-known (fail-closed for preventive)")
}

// A preventive file-guard's revalidation key is namespaced so it cannot pool with
// an old-format guardrail of the same folder name.
func TestFileGuardRevKey_Namespaced(t *testing.T) {
	assert.Equal(t, "file-guard:no-secrets", fileGuardRevKey("no-secrets"))
	assert.NotEqual(t, "no-secrets", fileGuardRevKey("no-secrets"),
		"a file-guard's key must not collide with an old-format guardrail's bare name")
}

// preventiveFileGuards keeps only the preventive ones — the set that acts at
// pre-tool.
func TestPreventiveFileGuards(t *testing.T) {
	guards := []declaration.FileGuard{
		{Name: "always-fine", Preventive: true},
		{Name: "fine-at-end", Preventive: false},
	}
	out := preventiveFileGuards(guards)
	require.Len(t, out, 1)
	assert.Equal(t, "always-fine", out[0].Name)
}

// A preventive guard on a PreFileUpdate whose result the engine could NOT compute
// (a command-derived update, resultKnown absent/false) fails CLOSED — it refuses
// the write, because a preventive guard cannot admit a write it cannot verify.
// The guard's check never runs (there is nothing to verify), so a check that
// would PASS still yields a refusal.
func TestRunFileGuardsPreventive_AbsentResultFailsClosed(t *testing.T) {
	guard := declaration.FileGuard{
		Name:       "always-fine",
		Match:      "src/**",
		Preventive: true,
		// A check that would PASS if it ran — proving the refusal is the
		// absent-result fail-closed, not the check.
		Checks: []declaration.Check{{Script: "./ok.sh"}},
		Dir:    t.TempDir(),
	}
	// PreFileUpdate under src/ with NO resultKnown → the engine could not compute
	// the write's outcome.
	e := event.Event{Kind: declaration.KindPreFileUpdate, Fields: map[string]any{
		filemod.FieldPath: "src/x.go",
	}}
	seeded := map[string]natures.ContextState{}

	reason := runFileGuardsPreventive(discard(), []declaration.FileGuard{guard}, []event.Event{e}, hookScope{}, seeded)
	require.NotEmpty(t, reason, "a preventive guard that cannot verify an update must refuse")
	assert.Contains(t, reason, "always-fine")
	assert.Contains(t, reason, "could not")
}

// A preventive guard on a PreFileUpdate whose result IS known runs its check
// normally (no fail-closed shortcut), so a passing check admits.
func TestRunFileGuardsPreventive_KnownResultRunsCheck(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "ok.sh", "#!/bin/sh\ncat >/dev/null\nexit 0\n")
	guard := declaration.FileGuard{
		Name:       "always-fine",
		Match:      "src/**",
		Preventive: true,
		Checks:     []declaration.Check{{Script: "./ok.sh"}},
		Dir:        dir,
	}
	// resultKnown true and newContent present → the guard verifies normally.
	e := event.Event{Kind: declaration.KindPreFileUpdate, Fields: map[string]any{
		filemod.FieldPath:        "src/x.go",
		filemod.FieldResultKnown: true,
		filemod.FieldNewContent:  "package x",
	}}
	reason := runFileGuardsPreventive(discard(), []declaration.FileGuard{guard}, []event.Event{e}, hookScope{}, map[string]natures.ContextState{})
	assert.Empty(t, reason, "a known-result update with a passing check is admitted")
}

// A preventive guard on a PreFileCreate whose result the engine could NOT derive
// — a NotebookEdit creating a fresh .ipynb, whose cell source is not the document
// (resultKnown false) — fails CLOSED, exactly as the underivable-update case does.
// This is the hole this fix closes: before it, only the update case failed closed,
// so an underivable create false-passed and the write landed transiently. The
// guard's check would PASS if it ran, so the refusal is the fail-closed shortcut.
func TestRunFileGuardsPreventive_UnderivableCreateFailsClosed(t *testing.T) {
	guard := declaration.FileGuard{
		Name:       "always-fine",
		Match:      "notebooks/**",
		Preventive: true,
		Checks:     []declaration.Check{{Script: "./ok.sh"}},
		Dir:        t.TempDir(),
	}
	// PreFileCreate under notebooks/ with an empty newContent and resultKnown
	// FALSE → the notebook create whose bytes are not derivable.
	e := event.Event{Kind: declaration.KindPreFileCreate, Fields: map[string]any{
		filemod.FieldPath:        "notebooks/fresh.ipynb",
		filemod.FieldNewContent:  "",
		filemod.FieldResultKnown: false,
	}}

	reason := runFileGuardsPreventive(discard(), []declaration.FileGuard{guard}, []event.Event{e}, hookScope{}, map[string]natures.ContextState{})
	require.NotEmpty(t, reason, "a preventive guard that cannot verify an underivable create must refuse (not pass)")
	assert.Contains(t, reason, "always-fine")
	assert.Contains(t, reason, "could not")
	assert.Contains(t, reason, "create", "the refusal names the create it could not verify")
}

// A preventive guard on a DERIVABLE create — a stated body, including a
// genuinely-empty one (resultKnown true) — runs its check normally rather than
// failing closed, so a passing check admits. This is the control that keeps the
// underivable-create refusal from swallowing every empty create: an empty file
// whose emptiness is KNOWN is a real result the guard may legitimately judge.
func TestRunFileGuardsPreventive_GenuineEmptyCreateRunsCheck(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "ok.sh", "#!/bin/sh\ncat >/dev/null\nexit 0\n")
	guard := declaration.FileGuard{
		Name:       "always-fine",
		Match:      "notebooks/**",
		Preventive: true,
		Checks:     []declaration.Check{{Script: "./ok.sh"}},
		Dir:        dir,
	}
	// A genuinely-empty create: newContent "" but resultKnown TRUE (a stated
	// empty body). The result is known, so the guard verifies normally.
	e := event.Event{Kind: declaration.KindPreFileCreate, Fields: map[string]any{
		filemod.FieldPath:        "notebooks/empty.md",
		filemod.FieldNewContent:  "",
		filemod.FieldResultKnown: true,
	}}
	reason := runFileGuardsPreventive(discard(), []declaration.FileGuard{guard}, []event.Event{e}, hookScope{}, map[string]natures.ContextState{})
	assert.Empty(t, reason, "a known-result (genuinely-empty) create with a passing check is admitted, not refused")
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
