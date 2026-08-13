package filemod

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
)

// fakeObserved is a difference already established against the baseline, built
// inline. Only the three methods the module reads are needed.
type fakeObserved struct {
	root  string
	paths []string
	// before is the set of paths that were present at the baseline. A path
	// absent from the map was not there.
	before map[string]bool
}

func (o fakeObserved) Root() string    { return o.root }
func (o fakeObserved) Paths() []string { return o.paths }
func (o fakeObserved) ExistedAtBaseline(path string) bool {
	return o.before[path]
}

// tree lays out a root with the named files present on disk, and returns it.
func tree(t *testing.T, present ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range present {
		full := filepath.Join(root, p)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte("content of "+p+"\n"), 0o644))
	}
	return root
}

// observe runs the post phase over an already-established difference.
func observe(t *testing.T, o fakeObserved) []event.Event {
	t.Helper()
	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePost,
		module.InputPayload: o,
	})
	require.NoError(t, err)
	return events
}

// --- the three classifications ----------------------------------------------

func TestObserved_AbsentBeforeAndPresentNowIsACreate(t *testing.T) {
	root := tree(t, "new.md")

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"new.md"},
		before: nil, // it was not there at the baseline
	})

	require.Len(t, events, 1)
	assert.Equal(t, KindPostCreate, events[0].Kind)
	assert.Equal(t, "new.md", events[0].Fields[FieldPath])
}

func TestObserved_PresentBeforeAndPresentNowIsAnUpdate(t *testing.T) {
	root := tree(t, "existing.md")

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"existing.md"},
		before: map[string]bool{"existing.md": true},
	})

	require.Len(t, events, 1)
	assert.Equal(t, KindPostUpdate, events[0].Kind)
	assert.Equal(t, "existing.md", events[0].Fields[FieldPath])
}

func TestObserved_PresentBeforeAndGoneNowIsADelete(t *testing.T) {
	// Nothing is written to the tree: the file is gone, which is the case that
	// breaks code assuming it can read what it reports.
	root := tree(t)

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"gone.md"},
		before: map[string]bool{"gone.md": true},
	})

	require.Len(t, events, 1)
	assert.Equal(t, KindPostDelete, events[0].Kind)
	assert.Equal(t, "gone.md", events[0].Fields[FieldPath])
}

func TestObserved_AbsentBeforeAndGoneNowIsNoEvent(t *testing.T) {
	// Created and removed inside the same cycle: no difference against the
	// baseline and nothing on disk, so there is no file for a rule to be about.
	root := tree(t)

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"scratch.tmp"},
		before: nil,
	})

	assert.Empty(t, events)
}

func TestClassify_EveryCombination(t *testing.T) {
	// The table the three tests above cover one row each, asserted directly so
	// a change to the rule shows up here rather than only through the tree.
	for _, tc := range []struct {
		before, now bool
		kind        string
		reportable  bool
	}{
		{before: false, now: true, kind: KindPostCreate, reportable: true},
		{before: true, now: true, kind: KindPostUpdate, reportable: true},
		{before: true, now: false, kind: KindPostDelete, reportable: true},
		{before: false, now: false, kind: "", reportable: false},
	} {
		kind, reportable := classify(tc.before, tc.now)
		assert.Equal(t, tc.reportable, reportable, "before=%v now=%v", tc.before, tc.now)
		assert.Equal(t, tc.kind, kind, "before=%v now=%v", tc.before, tc.now)
	}
}

// --- what a delete carries ---------------------------------------------------

func TestObserved_DeleteCarriesPathAndNothingElse(t *testing.T) {
	// There is no content to read for a deleted file, and the spec declares
	// PostFileDelete with a path and no more. A field the spec does not declare
	// is one no matcher can compile against.
	root := tree(t)

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"gone.md"},
		before: map[string]bool{"gone.md": true},
	})

	require.Len(t, events, 1)
	assert.Equal(t, map[string]any{FieldPath: "gone.md"}, events[0].Fields)
}

func TestObserved_DeleteOfAFileWhoseParentIsAlsoGone(t *testing.T) {
	// A whole directory removed: the path cannot be stat'd and neither can
	// anything above it. Still a delete, and still no read of the file.
	root := tree(t)

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"pkg/deep/nested.go"},
		before: map[string]bool{"pkg/deep/nested.go": true},
	})

	require.Len(t, events, 1)
	assert.Equal(t, KindPostDelete, events[0].Kind)
	assert.Equal(t, "pkg/deep/nested.go", events[0].Fields[FieldPath])
}

// --- no Post kind carries content -------------------------------------------

func TestObserved_NoKindCarriesContent(t *testing.T) {
	// Including the create. Unlike PreFileCreate the file is on disk by the
	// time this runs, so a hook reads it there rather than having it copied
	// through every event — and the spec declares no content on any Post kind.
	root := tree(t, "created.md", "updated.md")

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"created.md", "updated.md", "deleted.md"},
		before: map[string]bool{"updated.md": true, "deleted.md": true},
	})

	require.Len(t, events, 3)
	for _, e := range events {
		assert.NotContains(t, e.Fields, FieldContent, "kind %s", e.Kind)
	}
}

// --- one event per file, in the order given ---------------------------------

func TestObserved_OneEventPerFile(t *testing.T) {
	root := tree(t, "a.md", "b.md")

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"a.md", "b.md", "c.md"},
		before: map[string]bool{"b.md": true, "c.md": true},
	})

	require.Len(t, events, 3)
	assert.Equal(t, []event.Event{
		{Kind: KindPostCreate, Fields: map[string]any{FieldPath: "a.md"}},
		{Kind: KindPostUpdate, Fields: map[string]any{FieldPath: "b.md"}},
		{Kind: KindPostDelete, Fields: map[string]any{FieldPath: "c.md"}},
	}, events)
}

func TestObserved_MixedCycleReportsEachPathOnce(t *testing.T) {
	root := tree(t, "keep.md", "new1.md", "new2.md")

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"new1.md", "keep.md", "old1.md", "new2.md", "old2.md"},
		before: map[string]bool{"keep.md": true, "old1.md": true, "old2.md": true},
	})

	byPath := map[string]string{}
	for _, e := range events {
		p := e.Fields[FieldPath].(string)
		_, seen := byPath[p]
		require.False(t, seen, "path %q reported twice", p)
		byPath[p] = e.Kind
	}
	assert.Equal(t, map[string]string{
		"new1.md": KindPostCreate,
		"new2.md": KindPostCreate,
		"keep.md": KindPostUpdate,
		"old1.md": KindPostDelete,
		"old2.md": KindPostDelete,
	}, byPath)
}

// --- what it leaves out ------------------------------------------------------

func TestObserved_UntouchedFilesStaySilent(t *testing.T) {
	// The tree holds far more than the cycle changed. Only what the difference
	// named produces an event — the module never walks the tree itself, which
	// is what keeps the first cycle from putting the whole repository in front
	// of every guardrail.
	root := tree(t, "changed.md", "untouched1.md", "untouched2.md", "vendor/dep.go")

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"changed.md"},
		before: map[string]bool{"changed.md": true},
	})

	require.Len(t, events, 1)
	assert.Equal(t, "changed.md", events[0].Fields[FieldPath])
}

func TestObserved_EmptyDifferenceProducesNothing(t *testing.T) {
	// A cycle that changed nothing needing judgement is ordinary, not an error.
	for name, paths := range map[string][]string{
		"nil":   nil,
		"empty": {},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Empty(t, observe(t, fakeObserved{root: tree(t), paths: paths}))
		})
	}
}

func TestObserved_EmptyPathIsSkipped(t *testing.T) {
	// An empty path names no file. Reported it would stat the root itself,
	// which exists, and turn into an update of nothing.
	root := tree(t, "real.md")

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"", "real.md"},
		before: map[string]bool{"real.md": true},
	})

	require.Len(t, events, 1)
	assert.Equal(t, "real.md", events[0].Fields[FieldPath])
}

// --- payloads this module cannot read ----------------------------------------

func TestObserved_PayloadIsNotObserved(t *testing.T) {
	for name, payload := range map[string]any{
		"absent":     nil,
		"a string":   "not a difference",
		"a map":      map[string]any{"paths": []string{"a.md"}},
		"a slice":    []string{"a.md"},
		"an int":     42,
		"only paths": struct{ Paths []string }{Paths: []string{"a.md"}},
	} {
		t.Run(name, func(t *testing.T) {
			in := module.Input{module.InputPhase: module.PhasePost}
			if payload != nil {
				in[module.InputPayload] = payload
			}

			events, err := New().Extract(in)
			require.NoError(t, err, "an unreadable payload is not an error")
			assert.Empty(t, events)
		})
	}
}

// --- classification does not consult the pending prediction ------------------

func TestObserved_ClassificationIgnoresWhatAToolClaimed(t *testing.T) {
	// The same path, the same tree, classified purely on the baseline fact. A
	// write tool that announced a create over a file that was already there
	// produces an update here, because nothing in this path reads the claim.
	root := tree(t, "overwritten.md")

	asCreate := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"overwritten.md"},
		before: nil,
	})
	asUpdate := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"overwritten.md"},
		before: map[string]bool{"overwritten.md": true},
	})

	require.Len(t, asCreate, 1)
	require.Len(t, asUpdate, 1)
	assert.Equal(t, KindPostCreate, asCreate[0].Kind)
	assert.Equal(t, KindPostUpdate, asUpdate[0].Kind,
		"present at the baseline makes it an update, whatever produced the change")
}

// --- the events survive the module's own round trip --------------------------

func TestObserved_EventsRoundTripThroughFromEvent(t *testing.T) {
	root := tree(t, "a.md")

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"a.md", "gone.md"},
		before: map[string]bool{"gone.md": true},
	})
	require.Len(t, events, 2)

	for _, e := range events {
		f, err := FromEvent(e)
		require.NoError(t, err, "kind %s", e.Kind)
		assert.NotEmpty(t, f.Path)
		assert.Empty(t, f.Content, "kind %s carries no content", e.Kind)
	}
}

// --- every kind produced is one the module declared --------------------------

func TestObserved_ProducesOnlyDeclaredKinds(t *testing.T) {
	// A kind the module did not declare is one no matcher could have been
	// checked against at load, so it would dispatch to nothing.
	declared := map[string]bool{}
	for _, k := range New().Kinds() {
		declared[k.Name] = true
	}

	root := tree(t, "a.md", "b.md")
	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"a.md", "b.md", "c.md"},
		before: map[string]bool{"b.md": true, "c.md": true},
	})

	require.Len(t, events, 3)
	for _, e := range events {
		assert.True(t, declared[e.Kind], "undeclared kind %q", e.Kind)
	}
}
