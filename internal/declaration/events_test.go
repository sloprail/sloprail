package declaration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/module/modules"
)

// These tests pin the per-nature event vocabularies and the alias expansions to
// the spec's own unions (sloprail-service/events/main.tsp: GateEventKind,
// ContextEventKind) and to the aliases the two trigger docs define. A drift
// between what this package admits and what the spec says a nature may bind to is
// caught here rather than by a declaration silently loading or refusing.

// gateEventKinds is exactly the spec's GateEventKind union: the pre-action
// file/command/tool events plus Stop, and no Post event.
func TestEventKinds_GateMatchesSpecUnion(t *testing.T) {
	want := map[string]bool{
		"PreFileCreate":    true,
		"PreFileUpdate":    true,
		"PreFileDelete":    true,
		"PreCommandInvoke": true,
		"PreToolUse":       true,
		"Stop":             true,
	}
	assert.Equal(t, want, gateEventKinds, "gate vocabulary must equal the spec's GateEventKind union")
}

// contextEventKinds is exactly the spec's ContextEventKind union: every
// pre-action kind a gate has EXCEPT Stop, plus the Post file events and
// PostTagWrite.
func TestEventKinds_ContextMatchesSpecUnion(t *testing.T) {
	want := map[string]bool{
		"PreFileCreate":    true,
		"PreFileUpdate":    true,
		"PreFileDelete":    true,
		"PreCommandInvoke": true,
		"PreToolUse":       true,
		"PostFileCreate":   true,
		"PostFileUpdate":   true,
		"PostFileDelete":   true,
		"PostTagWrite":     true,
	}
	assert.Equal(t, want, contextEventKinds, "context vocabulary must equal the spec's ContextEventKind union")
}

// A gate does NOT admit Stop's exclusion — Stop is the one non-action event a
// gate carries, and a context is the one that excludes it. The two differ in
// exactly Stop and the Post events, which this asserts directly.
func TestEventKinds_GateAndContextDifferByStopAndPostEvents(t *testing.T) {
	assert.True(t, gateEventKinds["Stop"], "a gate may bind Stop")
	assert.False(t, contextEventKinds["Stop"], "a context may not enter on Stop")

	for _, post := range []string{"PostFileCreate", "PostFileUpdate", "PostFileDelete", "PostTagWrite"} {
		assert.False(t, gateEventKinds[post], "a gate may not bind %s", post)
	}
	for _, post := range []string{"PostFileCreate", "PostFileUpdate", "PostFileDelete", "PostTagWrite"} {
		assert.True(t, contextEventKinds[post], "a context may enter on %s", post)
	}
}

// Every concrete kind this package admits is one a shipped module actually
// declares — so the loader never admits an `on:` kind the engine cannot produce.
// This is the cross-check between the static vocabulary here and the live module
// registry.
func TestEventKinds_EveryAdmittedKindIsDeclaredByAModule(t *testing.T) {
	reg, err := modules.Registry()
	require.NoError(t, err)
	declared := make(map[string]bool)
	for _, k := range reg.DeclaredKinds() {
		declared[k] = true
	}
	for kind := range gateEventKinds {
		assert.True(t, declared[kind], "gate kind %q must be declared by a shipped module", kind)
	}
	for kind := range contextEventKinds {
		assert.True(t, declared[kind], "context kind %q must be declared by a shipped module", kind)
	}
}

// ---------------------------------------------------------------------------
// Alias expansion
// ---------------------------------------------------------------------------

// PreFileWrite expands to PreFileCreate + PreFileUpdate on a gate — the spec's
// GateDeclaration.on doc (~854) and GateTrigger doc (~713).
func TestExpandGateEvent_PreFileWriteAlias(t *testing.T) {
	kinds, known := expandGateEvent(AliasPreFileWrite)
	require.True(t, known)
	assert.Equal(t, []string{KindPreFileCreate, KindPreFileUpdate}, kinds)
}

// A concrete gate kind expands to itself.
func TestExpandGateEvent_ConcreteKind(t *testing.T) {
	kinds, known := expandGateEvent(KindPreCommandInvoke)
	require.True(t, known)
	assert.Equal(t, []string{KindPreCommandInvoke}, kinds)
}

// PostFileWrite is NOT a gate alias — a gate does not wake on settled content.
func TestExpandGateEvent_PostFileWriteRejected(t *testing.T) {
	_, known := expandGateEvent(AliasPostFileWrite)
	assert.False(t, known, "PostFileWrite is a context alias, not a gate one")
}

// A Post event is not a gate kind.
func TestExpandGateEvent_PostEventRejected(t *testing.T) {
	_, known := expandGateEvent(KindPostFileCreate)
	assert.False(t, known)
}

// PreFileWrite expands the same on a context.
func TestExpandContextEvent_PreFileWriteAlias(t *testing.T) {
	kinds, known := expandContextEvent(AliasPreFileWrite)
	require.True(t, known)
	assert.Equal(t, []string{KindPreFileCreate, KindPreFileUpdate}, kinds)
}

// PostFileWrite expands to PostFileCreate + PostFileUpdate on a context — the
// spec's ContextTrigger doc (~688).
func TestExpandContextEvent_PostFileWriteAlias(t *testing.T) {
	kinds, known := expandContextEvent(AliasPostFileWrite)
	require.True(t, known)
	assert.Equal(t, []string{KindPostFileCreate, KindPostFileUpdate}, kinds)
}

// A context does not enter on Stop even though a gate does.
func TestExpandContextEvent_StopRejected(t *testing.T) {
	_, known := expandContextEvent(KindStop)
	assert.False(t, known)
}

// An unknown name is unknown on both.
func TestExpandEvent_UnknownName(t *testing.T) {
	_, known := expandGateEvent("PreFileWirte")
	assert.False(t, known)
	_, known = expandContextEvent("Nonsense")
	assert.False(t, known)
}

// The diagnostic name lists include the aliases, so an author who mistyped one
// sees the alias among the alternatives.
func TestEventNames_IncludeAliases(t *testing.T) {
	assert.Contains(t, gateEventNames(), AliasPreFileWrite)
	assert.NotContains(t, gateEventNames(), AliasPostFileWrite, "PostFileWrite is not a gate name")

	assert.Contains(t, contextEventNames(), AliasPreFileWrite)
	assert.Contains(t, contextEventNames(), AliasPostFileWrite)
}
