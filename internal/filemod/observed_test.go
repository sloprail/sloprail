package filemod

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/grounding"
	"github.com/sloprail/sloprail/internal/module"
)

// fakeObserved is a difference already established against the baseline, built
// inline. Only the four methods the module reads are needed.
type fakeObserved struct {
	root  string
	paths []string
	// before is the set of paths that were present at the baseline. A path
	// absent from the map was not there.
	before map[string]bool
	// baselineContent is the bytes each path held at the baseline. Optional: a
	// path present at the baseline but absent here reads back as an empty,
	// readable blob, which is all most tests need — they assert Kind and Path,
	// not what the prior bytes were. A test that cares about oldContent supplies
	// the value here.
	baselineContent map[string]string
}

func (o fakeObserved) Root() string    { return o.root }
func (o fakeObserved) Paths() []string { return o.paths }
func (o fakeObserved) ExistedAtBaseline(path string) bool {
	return o.before[path]
}

// BaselineContent returns the file's bytes at the baseline, and whether they
// could be read. A create is never asked (it declares no oldContent); an update
// or delete gets its supplied content, or an empty-but-readable blob when the
// baseline held the path but the test named no bytes for it.
func (o fakeObserved) BaselineContent(path string) (string, bool) {
	if c, ok := o.baselineContent[path]; ok {
		return c, true
	}
	return "", o.before[path]
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

// observe runs the post phase over an already-established difference and
// requires that nothing about it was wrong.
func observe(t *testing.T, o fakeObserved) []event.Event {
	t.Helper()
	events, err := observeErr(o)
	require.NoError(t, err)
	return events
}

// observeErr is the same, for the cases whose point is what got reported.
func observeErr(o fakeObserved) ([]event.Event, error) {
	return New().Extract(module.Input{
		module.InputPhase:   module.PhasePost,
		module.InputPayload: o,
	})
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
	// No event — but the row is also what a producer answering ExistedAtBaseline
	// wrongly looks like, and the two are indistinguishable on the tree, so it
	// is reported rather than dropped.
	root := tree(t)

	events, err := observeErr(fakeObserved{
		root:   root,
		paths:  []string{"scratch.tmp"},
		before: nil,
	})

	assert.Empty(t, events)
	assert.ErrorIs(t, err, ErrNotADifference)
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

func TestObserved_DeleteCarriesTheBytesAboutToBeLost(t *testing.T) {
	// There is nothing on disk to read for a deleted file, so newContent and
	// newMarkers are not declared — but the spec declares PostFileDelete with
	// oldContent and oldMarkers, the bytes about to be lost and their markers,
	// which come from the session baseline the producer still holds.
	root := tree(t)

	events := observe(t, fakeObserved{
		root:            root,
		paths:           []string{"gone.md"},
		before:          map[string]bool{"gone.md": true},
		baselineContent: map[string]string{"gone.md": "# sr:doc gone.thing\nbody\n"},
	})

	require.Len(t, events, 1)
	assert.Equal(t, map[string]any{
		FieldPath:       "gone.md",
		FieldOldContent: "# sr:doc gone.thing\nbody\n",
		FieldOldMarkers: []any{
			map[string]any{KeyMarkerKind: "doc", KeyMarkerFQN: "gone.thing", KeyMarkerLine: 1},
		},
		FieldSeen:                false, // the module never knows; the session sets it
		grounding.FieldCitations: []any{},
	}, events[0].Fields,
		"a delete carries the baseline bytes and their markers, and no result fields")
	assert.NotContains(t, events[0].Fields, FieldNewContent, "a delete leaves no result")
	assert.NotContains(t, events[0].Fields, FieldNewMarkers, "a delete leaves nothing to scan")
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

// --- each Post kind carries the contents its declaration names --------------

func TestObserved_EachKindCarriesItsDeclaredContents(t *testing.T) {
	// A create has only newContent (read from disk, where the file now sits); a
	// delete has only oldContent (the baseline bytes, no longer on disk); an
	// update has both. The old model carried content on no Post kind; the new one
	// carries it exactly where the declaration says, so a create never asks the
	// baseline and a delete never reads the disk.
	root := tree(t, "created.md", "updated.md")

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"created.md", "updated.md", "deleted.md"},
		before: map[string]bool{"updated.md": true, "deleted.md": true},
		baselineContent: map[string]string{
			"updated.md": "old updated body\n",
			"deleted.md": "old deleted body\n",
		},
	})

	require.Len(t, events, 3)
	byPath := map[string]event.Event{}
	for _, e := range events {
		byPath[e.Fields[FieldPath].(string)] = e
	}

	create := byPath["created.md"]
	assert.Equal(t, KindPostCreate, create.Kind)
	assert.Equal(t, "content of created.md\n", create.Fields[FieldNewContent],
		"a create's newContent is read from disk as it now sits")
	assert.NotContains(t, create.Fields, FieldOldContent, "a create has no prior bytes")

	update := byPath["updated.md"]
	assert.Equal(t, KindPostUpdate, update.Kind)
	assert.Equal(t, "old updated body\n", update.Fields[FieldOldContent],
		"an update's oldContent is the session baseline's")
	assert.Equal(t, "content of updated.md\n", update.Fields[FieldNewContent],
		"and its newContent is what is on disk now")

	del := byPath["deleted.md"]
	assert.Equal(t, KindPostDelete, del.Kind)
	assert.Equal(t, "old deleted body\n", del.Fields[FieldOldContent],
		"a delete's oldContent is the baseline bytes about to be lost")
	assert.NotContains(t, del.Fields, FieldNewContent, "a delete leaves no result")
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
		{Kind: KindPostCreate, Fields: map[string]any{
			FieldPath:                "a.md",
			FieldNewContent:          "content of a.md\n",
			FieldNewMarkers:          []any{},
			FieldSeen:                false,
			grounding.FieldCitations: []any{},
		}},
		{Kind: KindPostUpdate, Fields: map[string]any{
			FieldPath:                "b.md",
			FieldOldContent:          "", // no baseline content supplied
			FieldNewContent:          "content of b.md\n",
			FieldOldMarkers:          []any{},
			FieldNewMarkers:          []any{},
			FieldSeen:                false,
			grounding.FieldCitations: []any{},
		}},
		{Kind: KindPostDelete, Fields: map[string]any{
			FieldPath:                "c.md",
			FieldOldContent:          "",
			FieldOldMarkers:          []any{},
			FieldSeen:                false,
			grounding.FieldCitations: []any{},
		}},
	}, events)
}

func TestObserved_MixedCycleReportsEachPathOnce(t *testing.T) {
	root := tree(t, "keep.md", "new1.md", "new2.md")

	// The input repeats paths, and repeats one of them under a second spelling.
	// Without that the "reported twice" assertion below can never fire and the
	// test only reads as coverage of the thing its name claims.
	events := observe(t, fakeObserved{
		root: root,
		paths: []string{
			"new1.md", "keep.md", "old1.md", "new2.md", "old2.md",
			"new1.md",   // the same path again
			"./keep.md", // the same file, spelled differently
			"old1.md",   // a repeated delete
		},
		before: map[string]bool{
			// One entry for keep.md and no entry for "./keep.md": the baseline
			// is asked with the canonical spelling, so one file needs one key
			// however many ways the producer spelled it.
			"keep.md": true, "old1.md": true, "old2.md": true,
		},
	})

	require.Len(t, events, 5, "eight paths naming five files")

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

// --- one file, one baseline, whatever order the spellings arrive in ----------

// recordingObserved notes every spelling ExistedAtBaseline was asked with, so a
// test can assert on the question rather than only on the answer.
type recordingObserved struct {
	fakeObserved
	asked *[]string
}

func (o recordingObserved) ExistedAtBaseline(path string) bool {
	*o.asked = append(*o.asked, path)
	return o.fakeObserved.ExistedAtBaseline(path)
}

func TestObserved_BaselineIsAskedWithTheCanonicalSpelling(t *testing.T) {
	// The contract says which spelling, and nothing but this test holds it
	// there. Asked with the raw path instead, everything still passes and the
	// order-dependence below comes back.
	root := tree(t, "dir/a.md")

	var asked []string
	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePost,
		module.InputPayload: recordingObserved{
			fakeObserved: fakeObserved{
				root:   root,
				paths:  []string{"./dir/./a.md"},
				before: map[string]bool{filepath.Join("dir", "a.md"): true},
			},
			asked: &asked,
		},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join("dir", "a.md")}, asked,
		"asked with the cleaned path, the same one the event carries")
	require.Len(t, events, 1)
	assert.Equal(t, KindPostUpdate, events[0].Kind)
}

// TestObserved_ACanonicalPathIsAskedAboutOnce pins the CALL COUNT, which the
// sibling test above does not: it asserts which spelling the producer is asked
// with, and would be satisfied by a module that asked with that spelling twice.
//
// The contract states the count in prose — `Observed.ExistedAtBaseline` says
// "both are asked" only "where a path's raw and canonical spellings differ", and
// checkBaselineSpelling's doc comment says asking twice "would make the module
// put two questions to the producer about one file where the contract has
// exactly one". Until this test, nothing held that.
//
// What holds it in the code is one term. checkBaselineSpelling reads:
//
//	if path == clean || before || !observed.ExistedAtBaseline(path) {
//
// and `path == clean` is the short-circuit: for an already-canonical path — which
// is every path git reports, so the common case — it returns before asking a
// second question. Deleting that term survives the whole suite on the VERDICT
// axis, because the two calls return the same answer for the same string. Only
// the count changes, and nothing was counting.
//
// That was found by mutation testing (mutant F1) rather than by a failing test,
// and it is the shape this project has been bitten by before: a property
// asserted in prose, load-bearing, and unmeasured. The sibling mutants on the
// same condition — dropping `before`, inverting the raw answer — both die by
// test, so the guard's other two terms were already pinned and this one was not.
//
// No bug is being fixed here. Today's only producer is a map read, so a second
// call costs nothing and cannot disagree with the first. What the test buys is
// that the doc comment stops being a claim nobody checks, and a future producer
// for which the count DOES matter — one that is expensive, logged, or
// non-idempotent — is not silently given two questions.
func TestObserved_ACanonicalPathIsAskedAboutOnce(t *testing.T) {
	root := tree(t, "a.md")

	var asked []string
	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePost,
		module.InputPayload: recordingObserved{
			fakeObserved: fakeObserved{
				root: root,
				// Already canonical, which is what git reports and therefore
				// the path the short-circuit exists for.
				paths: []string{"a.md"},
				// NOT at the baseline, and that is what makes this test bite.
				//
				// The guard is `path == clean || before || Existed(path)`, and
				// the caller passes `before` in. With a file that WAS at the
				// baseline, `before` is true and the second term short-circuits
				// before the third is ever reached — so the mutant that drops
				// `path == clean` survives, and an earlier version of this test
				// did exactly that: it passed against the mutant and pinned
				// nothing. Measured, not reasoned.
				//
				// A file absent at the baseline makes `before` false, so only
				// `path == clean` stands between the module and a second
				// question about a path whose two spellings are one string.
				before: map[string]bool{},
			},
			asked: &asked,
		},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"a.md"}, asked,
		"one canonical path is one question: the raw and canonical spellings are "+
			"the same string, so there is no second spelling to check the producer's "+
			"keying against")
	require.Len(t, events, 1)
	assert.Equal(t, KindPostCreate, events[0].Kind,
		"absent at the baseline and present now is a create")
}

func TestObserved_TwoSpellingsOfOneFileClassifyTheSameEitherOrder(t *testing.T) {
	// Keyed on the raw spelling, whichever of these came first silently won the
	// slot: {"a.md", "./a.md"} gave an update and the reverse gave a create —
	// one file, one tree, one producer, opposite events by iteration order.
	for name, paths := range map[string][]string{
		"canonical first": {"a.md", "./a.md"},
		"canonical last":  {"./a.md", "a.md"},
		"neither is bare": {"./a.md", "dir/../a.md"},
	} {
		t.Run(name, func(t *testing.T) {
			root := tree(t, "a.md")

			events := observe(t, fakeObserved{
				root:   root,
				paths:  paths,
				before: map[string]bool{"a.md": true},
			})

			require.Len(t, events, 1, "two spellings, one file")
			assert.Equal(t, KindPostUpdate, events[0].Kind,
				"the file was at the baseline however the producer spelled it")
			assert.Equal(t, "a.md", events[0].Fields[FieldPath])
		})
	}
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

	events, err := observeErr(fakeObserved{
		root:   root,
		paths:  []string{"", "real.md"},
		before: map[string]bool{"real.md": true},
	})

	assert.ErrorIs(t, err, ErrPathNotRelativeToRoot)
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

	byPath := map[string]FileEvent{}
	for _, e := range events {
		f, err := FromEvent(e)
		require.NoError(t, err, "kind %s", e.Kind)
		assert.NotEmpty(t, f.Path)
		byPath[f.Path] = f
	}

	// The create carries newContent (read from disk) and no oldContent; the
	// delete carries oldContent (the baseline, empty here) and no newContent.
	assert.Equal(t, "content of a.md\n", byPath["a.md"].NewContent,
		"a create round-trips its newContent")
	assert.Empty(t, byPath["a.md"].OldContent, "a create carries no oldContent")
	assert.Empty(t, byPath["gone.md"].NewContent, "a delete carries no newContent")
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
