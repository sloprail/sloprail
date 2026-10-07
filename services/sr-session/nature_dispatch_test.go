package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
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

// loadDeclFrom writes the given nature-relative files (plus config.yaml when
// config is non-empty) into a fresh `.sloprail` root and loads them through the
// real declaration store — so a bound-kinds test exercises the true load path
// (parse → validate → disable) rather than a hand-built Loaded literal.
func loadDeclFrom(t *testing.T, reg *module.Registry, files map[string]string, config string) declaration.Loaded {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".sloprail")
	for rel, content := range files {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		mode := os.FileMode(0o644)
		if strings.HasSuffix(path, ".sh") {
			mode = 0o755
		}
		require.NoError(t, os.WriteFile(path, []byte(content), mode))
		require.NoError(t, os.Chmod(path, mode))
	}
	if config != "" {
		require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte(config), 0o644))
	}
	loaded, err := declaration.New(root).Load(reg)
	require.NoError(t, err)
	return loaded
}

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
	loaded := declaration.Loaded{Structures: []declaration.StructureGate{{}}}
	bound := natureBoundKinds(loaded)
	assert.Contains(t, bound, declaration.KindPreFileCreate)
	assert.Contains(t, bound, declaration.KindPreFileUpdate)
}

// gateOnFileWrite is a minimal valid gate (a require or checks is mandatory) on the
// PreFileWrite alias, for the bound-kinds tests that need a gate that asks for the
// create/update kinds.
const gateOnFileWrite = `on:
  - event: PreFileWrite
checks:
  - script: ./check.sh
`

// A DISABLED gate asks for nothing — the new-format successor to the old
// boundkinds_test's disabled-guardrail-asks-nothing.
//
// The property is invisible downstream: a disabled gate that DID contribute its
// kinds would be stopped again at dispatch (it is not in loaded.Gates, so nothing
// runs it), so the only observable difference is a module run to extract events
// nobody could act on — cost, which is exactly what the check exists to avoid. So
// it is pinned here, at the seam, not through the harness. It is loaded through the
// real store with a config `disabled:` entry rather than a hand-built empty Loaded,
// so it proves the WHOLE path — disable filters the gate out of loaded.Gates, and
// bound-kinds reads only loaded.Gates — rather than a tautology on an empty literal.
func TestNaturePreToolBoundKinds_ADisabledGateAsksForNothing(t *testing.T) {
	reg, err := modules.Registry()
	require.NoError(t, err)

	files := map[string]string{
		"gate/g/gate.yaml": gateOnFileWrite,
		"gate/g/check.sh":  "#!/bin/sh\nexit 0\n",
	}

	// Control: with the gate enabled, its PreFileWrite alias expands into the bound
	// kinds — without which the assertion below is satisfied by a gate that never
	// asked for anything in the first place.
	on := loadDeclFrom(t, reg, files, "")
	require.Len(t, on.Gates, 1, "the enabled gate must load")
	assert.Contains(t, naturePreToolBoundKinds(on), declaration.KindPreFileCreate,
		"an enabled gate on PreFileWrite must ask for the create kind")

	// Disabled via the project's own config: it drops out of loaded.Gates, so it
	// contributes no bound kind.
	off := loadDeclFrom(t, reg, files, "disabled:\n  - gate/g\n")
	require.Empty(t, off.Gates, "the disabled gate must not load")
	assert.Empty(t, naturePreToolBoundKinds(off),
		"a disabled gate's kinds were collected, so its module runs to produce events nothing will act on")
}

// A BROKEN declaration asks for nothing either — and this is where the new format
// PARTS WAYS with the old.
//
// The old boundkinds_test asserted the opposite: a broken guardrail's kinds WERE
// collected, because the old format let an author list kinds under `hooks:` and a
// broken rule still carried them (Problem.Event), so collecting them was the only
// way an event of that kind firing could report the rule as unenforced. The new
// format has no author-declared kinds and its Problem carries no event kind (see
// the Wave-3 audit §7): there is nothing on an Invalid to collect, and the per-kind
// "not guarding this action" report it fed is gone, replaced by the unscoped
// reportNatureInvalid. So the correct new-format behavior is the inverse — bound
// kinds are read ONLY from the sound loaded set, never from loaded.Invalid — and
// this pins it, so a future change that started mining Invalid for kinds (there are
// none to mine) would be caught.
func TestNaturePreToolBoundKinds_ABrokenDeclarationAsksForNothing(t *testing.T) {
	loaded := declaration.Loaded{
		Invalid: []declaration.Invalid{{
			Nature:   declaration.NatureGate,
			Name:     "broken",
			Problems: []declaration.Problem{{Kind: declaration.ErrBadMatch, Fault: declaration.FaultDeclaration}},
		}},
	}
	assert.Empty(t, naturePreToolBoundKinds(loaded),
		"a broken declaration carries no author-declared kind in the new format, so bound-kinds must read only the sound loaded set")
}

// matchingEvents wakes a gate when a fired event's kind is one its trigger
// expands to AND the trigger's match holds — and reports no match otherwise.
func TestMatchingEvents(t *testing.T) {
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
	fired, err := matchingEvents(discard(), reg, g, []event.Event{match}, nil)
	require.NoError(t, err)
	require.Len(t, fired, 1, "a create under memories/ wakes a PreFileWrite gate narrowed to memories/")
	assert.Equal(t, match.Kind, fired[0].Kind)

	// A create OUTSIDE memories/ does not match (the trigger's match narrows it).
	outside := event.Event{Kind: declaration.KindPreFileCreate, Fields: map[string]any{"path": "src/a.go"}}
	fired, err = matchingEvents(discard(), reg, g, []event.Event{outside}, nil)
	require.NoError(t, err)
	assert.Empty(t, fired, "a write outside the match does not wake the gate")

	// A Stop event does not match a PreFileWrite gate at all (wrong kind).
	stop := event.Event{Kind: declaration.KindStop, Fields: map[string]any{}}
	fired, err = matchingEvents(discard(), reg, g, []event.Event{stop}, nil)
	require.NoError(t, err)
	assert.Empty(t, fired, "a Stop does not wake a PreFileWrite gate")
}

// A gate with no match on its trigger wakes on every occurrence of the kind.
func TestMatchingEvents_NoMatchWakesAlways(t *testing.T) {
	reg, err := modules.Registry()
	require.NoError(t, err)

	g := declaration.Gate{Name: "g", On: []declaration.GateTrigger{{Event: declaration.KindStop}}}
	stop := event.Event{Kind: declaration.KindStop, Fields: map[string]any{}}
	fired, err := matchingEvents(discard(), reg, g, []event.Event{stop}, nil)
	require.NoError(t, err)
	assert.Len(t, fired, 1, "a Stop gate with no match wakes on a Stop")
}

// A gate trigger match that COMPILES but cannot be EVALUATED against the fired
// event surfaces the error rather than reading as "did not wake" — the gate-side
// of the fail-closed seam behind tests/e2e/harness/session/027 (post_matcher_error),
// pinned at the dispatch level.
//
// This is where a broken or adversarial trigger would otherwise silently DISABLE a
// gate: matchingEvents returning (nil, nil) means "no trigger matched, the
// gate stays asleep" — approval of the event it was bound to — while (nil,
// err) means the engine could not DECIDE. runGatesForEvents turns the error into a
// REFUSAL naming the gate; treating an unevaluable match as a non-wake is exactly
// the fail-OPEN this regressed to and was corrected for. The e2e proves the refusal
// end to end; this proves the error is produced (not swallowed) at the seam.
//
// `any(event.invocations, int(.bin) > 0)` is a shape that reaches the evaluation
// branch: int of a string is well-formed, so the trigger LOADS clean, and at run
// time the vm refuses int("npm"). It is the same expression 014 and 027 ride on
// the gate side. (An absent flag no longer errors: it reads as an empty list.)
// sr:proves matching/unevaluable-never-passes
func TestMatchingEvents_UnevaluableMatchErrorsNotSkip(t *testing.T) {
	reg, err := modules.Registry()
	require.NoError(t, err)

	g := declaration.Gate{
		Name: "npm-access",
		On: []declaration.GateTrigger{{
			Event: declaration.KindPreCommandInvoke,
			Match: `any(event.invocations, int(.bin) > 0)`,
		}},
	}
	// A command invocation: `int(.bin)` errors at evaluation.
	cmd := event.Event{Kind: declaration.KindPreCommandInvoke, Fields: map[string]any{
		"raw": "npm publish",
		"invocations": []any{map[string]any{
			"bin": "npm", "argv": []any{"npm", "publish"}, "flags": map[string]any{},
		}},
	}}

	fired, err := matchingEvents(discard(), reg, g, []event.Event{cmd}, nil)
	require.Error(t, err,
		"a trigger match that cannot be evaluated must surface the error (fail-closed), not be read as the gate not waking")
	assert.Empty(t, fired, "no clean wake is reported alongside the error")
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

// checkStructureGate combines every loaded structure gate: a plugin's scoped one
// refuses a create/update inside its scope, while a DELETE inside the same scope
// is not gated at all (a delete is not a write), and the refusal names the plugin.
// sr:proves structure/deletes-are-not-writes
func TestCheckStructureGate_PluginScopeGatesWritesNotDeletes(t *testing.T) {
	plugin := declaration.StructureGate{
		Scope:  []declaration.StructureEntry{{Glob: ".mdmap/"}},
		Allow:  []declaration.StructureEntry{{Glob: ".mdmap/mindmap/*/mindmap.yaml"}},
		Origin: declaration.Origin{Plugin: "mdmap", Root: "/plugins/mdmap"},
	}
	structures := []declaration.StructureGate{plugin}
	ev := func(kind, path string) []event.Event {
		return []event.Event{{Kind: kind, Fields: map[string]any{"path": path}}}
	}

	reason := checkStructureGate(discard(), structures, ev(declaration.KindPreFileCreate, ".mdmap/stray.md"), "", nil, "")
	assert.Contains(t, reason, `plugin "mdmap"`, "a create inside the scope that the plugin does not allow is refused, naming it")

	reason = checkStructureGate(discard(), structures, ev(declaration.KindPreFileUpdate, ".mdmap/stray.md"), "", nil, "")
	assert.NotEmpty(t, reason, "an update is a write too")

	reason = checkStructureGate(discard(), structures, ev(declaration.KindPreFileDelete, ".mdmap/stray.md"), "", nil, "")
	assert.Empty(t, reason, "a delete is not gated by the structure gate")

	reason = checkStructureGate(discard(), structures, ev(declaration.KindPreFileCreate, ".mdmap/mindmap/a/mindmap.yaml"), "", nil, "")
	assert.Empty(t, reason, "the allowed shape passes")

	reason = checkStructureGate(discard(), structures, ev(declaration.KindPreFileCreate, "src/main.go"), "", nil, "")
	assert.Empty(t, reason, "outside the plugin's scope, with no project structure, a write is permitted")
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
	nested := gateMatchEvent(e, nil)
	inner, ok := nested.Fields["event"].(map[string]any)
	require.True(t, ok, "the fired event's fields are nested under `event`")
	assert.Equal(t, "memories/a.md", inner["path"])
}

// A gate is woken once per matching PRE FILE event — every file a call changes —
// but once only for any other kind: two commands (or a command and a file) do not
// wake a one-shot command gate twice.
func TestMatchingEvents_EveryFileOfACallButOneOfAnythingElse(t *testing.T) {
	reg, err := modules.Registry()
	require.NoError(t, err)

	pre := func(kind, path string) event.Event {
		return event.Event{Kind: kind, Fields: map[string]any{"path": path}}
	}
	g := declaration.Gate{Name: "g", On: []declaration.GateTrigger{
		{Event: declaration.AliasPreFileWrite, Match: `event.path startsWith "src/"`},
		{Event: declaration.KindPreFileDelete, Match: `event.path startsWith "src/"`},
	}}
	events := []event.Event{
		pre(declaration.KindPreFileCreate, "src/a.go"),
		pre(declaration.KindPreFileUpdate, "docs/x.md"),
		pre(declaration.KindPreFileDelete, "src/b.go"),
		pre(declaration.KindPreFileUpdate, "src/c.go"),
	}
	fired, err := matchingEvents(discard(), reg, g, events, nil)
	require.NoError(t, err)
	var paths []string
	for _, e := range fired {
		paths = append(paths, e.Fields["path"].(string))
	}
	assert.Equal(t, []string{"src/a.go", "src/b.go", "src/c.go"}, paths,
		"the gate is asked about every matching file, in order, and not about the one its match excludes")

	stops := []event.Event{{Kind: declaration.KindStop, Fields: map[string]any{}}, {Kind: declaration.KindStop, Fields: map[string]any{}}}
	stopGate := declaration.Gate{Name: "s", On: []declaration.GateTrigger{{Event: declaration.KindStop}}}
	fired, err = matchingEvents(discard(), reg, stopGate, stops, nil)
	require.NoError(t, err)
	assert.Len(t, fired, 1, "a non-file event wakes the gate once")
}
