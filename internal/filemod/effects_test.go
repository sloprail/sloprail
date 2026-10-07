package filemod

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/module"
)

// effectPending is a harness's pending call that states its file effects outright
// (Codex's apply_patch), rather than in a Write/Edit-shaped argument.
type effectPending struct {
	fakePending
	effects []harness.FileEffect
}

func (p effectPending) FileEffects() []harness.FileEffect { return p.effects }

func extractEffectsOf(t *testing.T, effects ...harness.FileEffect) []string {
	t.Helper()
	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: effectPending{fakePending: fakePending{tool: "apply_patch"}, effects: effects},
	})
	require.NoError(t, err)
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	return kinds
}

func TestExtractEffects_AreClassifiedAgainstTheTreeLikeAWrite(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.md")
	require.NoError(t, os.WriteFile(existing, []byte("old\n"), 0o644))
	fresh := filepath.Join(dir, "fresh.md")
	missing := filepath.Join(dir, "missing.md")

	assert.Equal(t, []string{KindPreCreate},
		extractEffectsOf(t, harness.FileEffect{Kind: harness.FileCreate, Path: fresh, NewContent: "x\n", ResultKnown: true}))
	assert.Equal(t, []string{KindPreUpdate},
		extractEffectsOf(t, harness.FileEffect{Kind: harness.FileCreate, Path: existing, NewContent: "x\n", ResultKnown: true}),
		"an add over an existing file overwrites it: an update")
	assert.Equal(t, []string{KindPreUpdate},
		extractEffectsOf(t, harness.FileEffect{Kind: harness.FileUpdate, Path: existing, NewContent: "new\n", ResultKnown: true}))
	assert.Equal(t, []string{KindPreDelete},
		extractEffectsOf(t, harness.FileEffect{Kind: harness.FileDelete, Path: existing}))

	assert.Empty(t, extractEffectsOf(t, harness.FileEffect{Kind: harness.FileUpdate, Path: missing}),
		"an update of a file that is not there fails in the tool: nothing is about to change")
	assert.Empty(t, extractEffectsOf(t, harness.FileEffect{Kind: harness.FileDelete, Path: missing}))
}

func TestExtractEffects_CarryTheResultAndSayWhetherItIsKnown(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.md")
	require.NoError(t, os.WriteFile(existing, []byte("old\n"), 0o644))

	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: effectPending{fakePending: fakePending{tool: "apply_patch"}, effects: []harness.FileEffect{
			{Kind: harness.FileUpdate, Path: existing, NewContent: "new\n", ResultKnown: true},
			{Kind: harness.FileUpdate, Path: existing + "2"},
		}},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "new\n", events[0].Fields[FieldNewContent])
	assert.Equal(t, "old\n", events[0].Fields[FieldOldContent])
	assert.Equal(t, true, events[0].Fields[FieldResultKnown])

	require.NoError(t, os.WriteFile(existing+"2", []byte("old\n"), 0o644))
	events, err = New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: effectPending{fakePending: fakePending{tool: "apply_patch"}, effects: []harness.FileEffect{
			{Kind: harness.FileUpdate, Path: existing + "2"},
		}},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, false, events[0].Fields[FieldResultKnown], "an underivable result must not read as an empty file")
}

func TestExtractEffects_AnEmptyListFallsBackToTheArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.md")
	args := writePending(path, "body")
	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: effectPending{fakePending: args},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, KindPreCreate, events[0].Kind)
}
