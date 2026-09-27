package filemod

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/grounding"
	"github.com/sloprail/sloprail/internal/module"
)

func TestFileEvent_Event_PathOnly(t *testing.T) {
	e := FileEvent{Path: "memories/a.md"}.Event(KindPreUpdate)

	assert.Equal(t, KindPreUpdate, e.Kind)
	assert.Equal(t, map[string]any{
		FieldPath: "memories/a.md",
		// oldContent and newContent both present and empty. PreFileUpdate
		// declares both, and presence is decided by the declaration rather than
		// by the value.
		FieldOldContent: "",
		FieldNewContent: "",
		// resultKnown present and false. A bare FileEvent has derived nothing, so
		// the pair a rule must consult together — `newContent == ""` and
		// `resultKnown` — is exactly what is carried here.
		FieldResultKnown: false,
		// Both markers lists present and empty. PreFileUpdate declares oldMarkers
		// and newMarkers; this FileEvent simply has none.
		FieldOldMarkers: []any{},
		FieldNewMarkers: []any{},
		// citations present and empty: the session attaches them, never this
		// module.
		grounding.FieldCitations: []any{},
	}, e.Fields)
}

func TestFileEvent_Event_WithContent(t *testing.T) {
	e := FileEvent{Path: "memories/a.md", NewContent: "# Notes\n", ResultKnown: true}.Event(KindPreCreate)

	assert.Equal(t, KindPreCreate, e.Kind)
	assert.Equal(t, map[string]any{
		FieldPath:                "memories/a.md",
		FieldNewContent:          "# Notes\n",
		FieldResultKnown:         true,
		FieldNewMarkers:          []any{},
		grounding.FieldCitations: []any{},
	}, e.Fields)
	assert.NotContains(t, e.Fields, FieldOldContent,
		"a create has no oldContent: nothing preceded it")
}

// TestFileEvent_Event_EmptyContentIsCarried states what is now true: newContent
// is carried whenever the kind declares it, empty or not, so a file whose
// content genuinely is the empty string produces an event with `newContent: ""`
// — and `newContent == ""`, the rule an author writes to catch an empty file,
// fires.
//
// This test previously asserted the opposite and pinned the defect. Content and
// markers are now carried by the same rule: the declaration decides presence,
// the value decides only what is held. `len(newMarkers) == 0` and `newContent
// == ""` are both real questions an author asks, and neither may error.
func TestFileEvent_Event_EmptyContentIsCarried(t *testing.T) {
	e := FileEvent{Path: "empty.md", NewContent: "", ResultKnown: true}.Event(KindPreCreate)

	require.Contains(t, e.Fields, FieldNewContent)
	assert.Equal(t, "", e.Fields[FieldNewContent])
	// path, newContent, resultKnown, newMarkers and citations — every field
	// PreFileCreate declares. resultKnown is carried too now, so an underivable
	// empty result is tellable from this genuinely-empty (resultKnown true) one.
	assert.Len(t, e.Fields, 5)
	assert.Contains(t, e.Fields, FieldNewMarkers)
	assert.Equal(t, true, e.Fields[FieldResultKnown])
}

func TestFileEvent_Event_KindIsPassedThroughUnchecked(t *testing.T) {
	// Event does not police the kind; the caller picks it, and the registry is
	// what decides whether a kind is one this module owns.
	e := FileEvent{Path: "a.md"}.Event("NotAFileKind")
	assert.Equal(t, "NotAFileKind", e.Kind)
}

// TestFileEvent_PathSurvivesAnUnknownKind guards the seam the declaration-driven
// emitter opened. kindDeclares answers false for every field of a kind it does
// not know, so keying path off the declaration made an unknown kind produce an
// event with no fields at all — one reporting a file without naming it, which
// is the silent nothing this engine exists to prevent. Path is unconditional
// for exactly this reason.
func TestFileEvent_PathSurvivesAnUnknownKind(t *testing.T) {
	e := FileEvent{Path: "a.md", NewContent: "x"}.Event("NotAFileKind")
	require.Contains(t, e.Fields, FieldPath, "an event must always name its file")
	assert.Equal(t, "a.md", e.Fields[FieldPath])
	assert.NotContains(t, e.Fields, FieldNewContent, "an unknown kind declares nothing else")
}

// TestModule_EveryDeclaredKindCarriesPath is the premise the unconditional path
// rests on: if a kind were ever declared WITHOUT path, the line above would be
// carrying a field that kind does not declare — the very defect being fixed.
func TestModule_EveryDeclaredKindCarriesPath(t *testing.T) {
	for _, k := range (&Module{}).Kinds() {
		var has bool
		for _, f := range k.Fields {
			if f.Name == FieldPath {
				has = true
			}
		}
		assert.Truef(t, has, "kind %q must declare %q", k.Name, FieldPath)
	}
}

func TestFileEvent_Event_FieldsAreFresh(t *testing.T) {
	// Two events built from one FileEvent must not share a fields map, or
	// mutating one would rewrite the other.
	f := FileEvent{Path: "a.md", NewContent: "x"}
	first := f.Event(KindPreCreate)
	second := f.Event(KindPreCreate)

	first.Fields[FieldPath] = "mutated"
	assert.Equal(t, "a.md", second.Fields[FieldPath])
}

func TestFromEvent_RoundTrip(t *testing.T) {
	for name, in := range map[string]FileEvent{
		"path only":        {Path: "memories/a.md"},
		"path and content": {Path: "memories/a.md", NewContent: "# Notes\n"},
		"unicode path":     {Path: "памʼять/файл.md", NewContent: "Правило ✅\n"},
		"content with nul": {Path: "a.bin", NewContent: "a\x00b"},
		"whitespace body":  {Path: "a.md", NewContent: "  \n\t\n"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := FromEvent(in.Event(KindPreCreate))
			require.NoError(t, err)

			// newMarkers does not round-trip nil: a FileEvent built without any
			// comes back with an EMPTY list, because the wire form carries the
			// field present-and-empty on every kind that declares it. That
			// asymmetry is the point — `len(newMarkers) == 0` must hold for a
			// file with no markers rather than error on an absent field — so it
			// is asserted rather than normalised away.
			assert.NotNil(t, got.NewMarkers, "the wire form is empty, not absent")
			assert.Empty(t, got.NewMarkers)

			got.NewMarkers = in.NewMarkers
			assert.Equal(t, in, got, "everything else round-trips exactly")
		})
	}
}

func TestFromEvent_ContentAbsentGivesEmptyString(t *testing.T) {
	got, err := FromEvent(event.Event{
		Kind:   KindPostUpdate,
		Fields: map[string]any{FieldPath: "a.md"},
	})
	require.NoError(t, err)
	assert.Equal(t, FileEvent{Path: "a.md"}, got)
	assert.Empty(t, got.NewContent)
	assert.Empty(t, got.OldContent)
}

func TestFromEvent_NoPathIsAnError(t *testing.T) {
	// Silently returning a zero value would let a caller act on a file that
	// was never named.
	cases := map[string]event.Event{
		"nil fields":   {Kind: KindPreCreate},
		"empty fields": {Kind: KindPreCreate, Fields: map[string]any{}},
		"empty path":   {Kind: KindPreCreate, Fields: map[string]any{FieldPath: ""}},
		"content only": {Kind: KindPreCreate, Fields: map[string]any{FieldNewContent: "x"}},
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := FromEvent(e)
			require.Error(t, err)
			assert.Equal(t, FileEvent{}, got, "the zero value is returned alongside the error")
			assert.Contains(t, err.Error(), "carries no "+FieldPath)
			assert.Contains(t, err.Error(), e.Kind, "the error names the kind it was given")
		})
	}
}

func TestFromEvent_WrongTypedPathIsAnError(t *testing.T) {
	// A non-string path fails the type assertion, leaving Path empty, which
	// lands in the same no-path error rather than a coerced value.
	for name, v := range map[string]any{
		"int":   42,
		"nil":   nil,
		"slice": []string{"a.md"},
		"bytes": []byte("a.md"),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := FromEvent(event.Event{
				Kind:   KindPreCreate,
				Fields: map[string]any{FieldPath: v},
			})
			require.Error(t, err)
			assert.Equal(t, FileEvent{}, got)
		})
	}
}

func TestFromEvent_WrongTypedContentIsIgnored(t *testing.T) {
	// CURRENT behaviour: a non-string content is dropped rather than reported,
	// because only the path is treated as load-bearing.
	got, err := FromEvent(event.Event{
		Kind: KindPreCreate,
		Fields: map[string]any{
			FieldPath:       "a.md",
			FieldNewContent: 42,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, FileEvent{Path: "a.md"}, got)
}

func TestFromEvent_IgnoresUnknownFields(t *testing.T) {
	got, err := FromEvent(event.Event{
		Kind: KindPreCreate,
		Fields: map[string]any{
			FieldPath:  "a.md",
			"whatever": "some other module's business",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, FileEvent{Path: "a.md"}, got)
}

// TestFromEvent_MarkersRoundTripOnBothSides checks that both marker lists are
// read back, keyed off the right field names — a create's newMarkers and a
// delete's oldMarkers land on the matching struct field, not swapped.
func TestFromEvent_MarkersRoundTripOnBothSides(t *testing.T) {
	create := FileEvent{Path: "a.go", NewContent: "// sr:endpoint api.get\n"}
	create.NewMarkers = Scan(create.NewContent)
	got, err := FromEvent(create.Event(KindPreCreate))
	require.NoError(t, err)
	assert.Equal(t, create.NewMarkers, got.NewMarkers)
	assert.Empty(t, got.OldMarkers, "a create carries no oldMarkers")

	del := FileEvent{Path: "a.go", OldContent: "// sr:endpoint api.get\n"}
	del.OldMarkers = Scan(del.OldContent)
	gotDel, err := FromEvent(del.Event(KindPreDelete))
	require.NoError(t, err)
	assert.Equal(t, del.OldMarkers, gotDel.OldMarkers)
	assert.Empty(t, gotDel.NewMarkers, "a delete carries no newMarkers")
}

// --- Kinds -------------------------------------------------------------------

func TestModule_Name(t *testing.T) {
	assert.Equal(t, Name, New().Name())
	assert.Equal(t, "file", Name, "the module name is not a prefix the kinds carry")
}

func TestModule_KindsAreDistinct(t *testing.T) {
	kinds := New().Kinds()
	require.Len(t, kinds, 6)

	seen := map[string]bool{}
	for _, k := range kinds {
		assert.NotEmpty(t, k.Name)
		assert.False(t, seen[k.Name], "kind %q declared twice", k.Name)
		seen[k.Name] = true
	}
	for _, want := range []string{
		KindPreCreate, KindPreUpdate, KindPreDelete,
		KindPostCreate, KindPostUpdate, KindPostDelete,
	} {
		assert.True(t, seen[want], "kind %q must be declared", want)
	}
}

// TestModule_FieldsPerKind pins the exact field layout of every kind against the
// spec (events/main.tsp). This is the one place the whole field model is stated,
// so a field added to or dropped from a kind is caught here rather than
// discovered by a rule that stops firing.
func TestModule_FieldsPerKind(t *testing.T) {
	fieldsOf := map[string][]string{}
	for _, k := range New().Kinds() {
		for _, f := range k.Fields {
			fieldsOf[k.Name] = append(fieldsOf[k.Name], f.Name)
		}
	}

	assert.Equal(t, []string{FieldPath, FieldNewContent, FieldResultKnown, FieldNewMarkers, grounding.FieldCitations},
		fieldsOf[KindPreCreate],
		"a create has no prior bytes — newContent, its newMarkers, and resultKnown (a notebook create's bytes are not derivable, so the empty result must be tellable from a genuinely-empty one)")
	assert.Equal(t, []string{FieldPath, FieldOldContent, FieldNewContent, FieldResultKnown, FieldOldMarkers, FieldNewMarkers, grounding.FieldCitations},
		fieldsOf[KindPreUpdate],
		"an update carries both contents; resultKnown makes an uncomputable newContent askable")
	assert.Equal(t, []string{FieldPath, FieldOldContent, FieldOldMarkers, grounding.FieldCitations},
		fieldsOf[KindPreDelete],
		"a delete carries only the bytes about to be lost")

	assert.Equal(t, []string{FieldPath, FieldNewContent, FieldNewMarkers, FieldSeen, grounding.FieldCitations},
		fieldsOf[KindPostCreate],
		"a Post create mirrors PreFileCreate, plus seen")
	assert.Equal(t, []string{FieldPath, FieldOldContent, FieldNewContent, FieldOldMarkers, FieldNewMarkers, FieldSeen, grounding.FieldCitations},
		fieldsOf[KindPostUpdate],
		"a Post update carries both settled contents, seen, and no resultKnown")
	assert.Equal(t, []string{FieldPath, FieldOldContent, FieldOldMarkers, FieldSeen, grounding.FieldCitations},
		fieldsOf[KindPostDelete],
		"a Post delete mirrors PreFileDelete, plus seen")
}

// TestModule_ResultKnownOnPreCreateAndUpdate: the companion boolean exists to
// make an uncomputable result's empty value tellable from a genuinely-empty one,
// and the two PRE kinds whose result can arrive either way are PreFileUpdate (a
// command-derived update) and PreFileCreate (a notebook create). A delete has no
// result, and the Post kinds are settled — none of those carry it.
func TestModule_ResultKnownOnPreCreateAndUpdate(t *testing.T) {
	declares := map[string]bool{}
	for _, k := range New().Kinds() {
		for _, f := range k.Fields {
			if f.Name == FieldResultKnown {
				declares[k.Name] = true
			}
		}
	}
	assert.Equal(t, map[string]bool{KindPreCreate: true, KindPreUpdate: true}, declares)
}

// TestModule_NewMarkersOnCreateAndUpdate / OldMarkersOnUpdateAndDelete: markers
// follow their content. newMarkers wherever there is a result to scan (create,
// update); oldMarkers wherever there is prior text (update, delete); a delete
// has no newMarkers and a create has no oldMarkers.
func TestModule_MarkersFollowTheirContent(t *testing.T) {
	newMarkers := map[string]bool{}
	oldMarkers := map[string]bool{}
	for _, k := range New().Kinds() {
		for _, f := range k.Fields {
			if f.Name == FieldNewMarkers {
				newMarkers[k.Name] = true
			}
			if f.Name == FieldOldMarkers {
				oldMarkers[k.Name] = true
			}
		}
	}
	assert.Equal(t, map[string]bool{
		KindPreCreate: true, KindPreUpdate: true,
		KindPostCreate: true, KindPostUpdate: true,
	}, newMarkers, "newMarkers wherever there is a result to scan")
	assert.Equal(t, map[string]bool{
		KindPreUpdate: true, KindPreDelete: true,
		KindPostUpdate: true, KindPostDelete: true,
	}, oldMarkers, "oldMarkers wherever there is prior text")
}

func TestModule_EveryDeclaredFieldIsTyped(t *testing.T) {
	// Typed with something this build recognises. A field whose type falls
	// through matcherEnv's default becomes types.Any, which is the unchecked
	// case this declaration exists to avoid.
	known := map[module.FieldType]bool{
		module.TypeString: true, module.TypeBool: true,
		module.TypeList: true, module.TypeMap: true, module.TypeInt: true,
	}
	for _, k := range New().Kinds() {
		for _, f := range k.Fields {
			assert.True(t, known[f.Type], "kind %q field %q has type %q", k.Name, f.Name, f.Type)
		}
	}
}

// TestModule_MarkersDeclareTheirElementShape: both markers fields declare the
// same closed element shape, so a typo inside a predicate over either is refused
// at load rather than silently never firing.
func TestModule_MarkersDeclareTheirElementShape(t *testing.T) {
	for _, fieldName := range []string{FieldOldMarkers, FieldNewMarkers} {
		var markers *module.FieldDecl
		for _, k := range New().Kinds() {
			for i, f := range k.Fields {
				if f.Name == fieldName {
					markers = &k.Fields[i]
				}
			}
		}
		require.NotNilf(t, markers, "no kind declares %q", fieldName)
		require.Equal(t, module.TypeList, markers.Type)
		require.NotNilf(t, markers.Elem, "%q: a nil Elem leaves the predicate body unchecked", fieldName)
		require.Equalf(t, module.TypeMap, markers.Elem.Type,
			"%q: only a TypeMap with Fields resolves to a closed structure in matcherenv", fieldName)

		byName := map[string]module.FieldType{}
		for _, f := range markers.Elem.Fields {
			byName[f.Name] = f.Type
		}
		assert.Equalf(t, map[string]module.FieldType{
			KeyMarkerKind: module.TypeString,
			KeyMarkerFQN:  module.TypeString,
			KeyMarkerLine: module.TypeInt,
		}, byName, "%q element keys", fieldName)
	}
}

func TestModule_KindsIsStable(t *testing.T) {
	assert.Equal(t, New().Kinds(), New().Kinds())
}

func TestFileEvent_MarkersAreCarriedExactlyWhereDeclared(t *testing.T) {
	// The claim in Event's doc comment: markers are keyed off the declaration,
	// so the wire form cannot carry them on a kind that does not declare them or
	// omit them on one that does. Checked against every kind and every markers
	// field, with a FileEvent that HOLDS markers, so a kind that leaked them
	// would be caught rather than passing on an empty struct.
	f := FileEvent{
		Path:       "a.go",
		OldMarkers: []Marker{{Kind: "k", FQN: "f", Line: 1}},
		NewMarkers: []Marker{{Kind: "k", FQN: "f", Line: 1}},
	}
	for _, fieldName := range []string{FieldOldMarkers, FieldNewMarkers} {
		for _, k := range New().Kinds() {
			declared := false
			for _, fd := range k.Fields {
				if fd.Name == fieldName {
					declared = true
				}
			}
			fields := f.Event(k.Name).Fields
			if declared {
				assert.Containsf(t, fields, fieldName, "kind %q declares %q but does not carry it", k.Name, fieldName)
				continue
			}
			assert.NotContainsf(t, fields, fieldName, "kind %q carries %q it does not declare", k.Name, fieldName)
		}
	}
}

// TestFileEvent_ContentIsOmittedFromKindsThatDoNotDeclareIt is the fixed form
// of a test that used to pin the opposite.
//
// Event once set content whenever it was non-empty without asking whether the
// kind declared it, so a FileEvent carrying content produced a PreFileDelete
// with a content field no matcher could be checked against — CompileMatcherFor
// validates names against the declaration and refuses one that is not there.
// Every field now goes through kindDeclares, so presence is the declaration's
// answer and never the value's. A delete declares no newContent; a create
// declares no oldContent.
func TestFileEvent_ContentIsOmittedFromKindsThatDoNotDeclareIt(t *testing.T) {
	f := FileEvent{Path: "a.go", OldContent: "old", NewContent: "new"}
	// newContent is not on a delete.
	assert.NotContains(t, f.Event(KindPreDelete).Fields, FieldNewContent,
		"a delete does not declare newContent and must not carry it")
	assert.NotContains(t, f.Event(KindPostDelete).Fields, FieldNewContent)
	// oldContent is not on a create.
	assert.NotContains(t, f.Event(KindPreCreate).Fields, FieldOldContent,
		"a create does not declare oldContent and must not carry it")
	assert.NotContains(t, f.Event(KindPostCreate).Fields, FieldOldContent)
}

// TestFileEvent_DeclaredFieldsAreAlwaysPresent is the general statement of the
// rule, checked against the declaration itself rather than a hand-listed set of
// kinds — so a kind added later is covered without this test being touched.
//
// The zero-valued FileEvent is the point. A field is carried because the kind
// declares it, never because the value happened to be interesting, and the
// empty value is exactly the case the old emitter dropped.
func TestFileEvent_DeclaredFieldsAreAlwaysPresent(t *testing.T) {
	for _, k := range (&Module{}).Kinds() {
		fields := FileEvent{}.Event(k.Name).Fields
		assert.Lenf(t, fields, len(k.Fields),
			"kind %q carries %d fields but declares %d", k.Name, len(fields), len(k.Fields))
		for _, fd := range k.Fields {
			assert.Containsf(t, fields, fd.Name,
				"kind %q declares %q but a zero-valued FileEvent omits it", k.Name, fd.Name)
		}
	}
}

// TestFileEvent_EmptyContentIsCarriedOnPreFileCreate is defect 1 exactly: the
// empty file. `newContent` is required on PreFileCreate (spec events/main.tsp),
// and a matcher written `newContent == ""` — the rule an author writes to catch
// an empty file — is the one that met a nil and errored.
func TestFileEvent_EmptyContentIsCarriedOnPreFileCreate(t *testing.T) {
	fields := FileEvent{Path: "empty.txt", NewContent: ""}.Event(KindPreCreate).Fields
	require.Contains(t, fields, FieldNewContent,
		"a genuinely empty file must still carry the content its kind declares")
	assert.Equal(t, "", fields[FieldNewContent])
}

func TestFileEvent_EveryKindsMarkersAreFreshPerCall(t *testing.T) {
	// Two events from one FileEvent must not share the markers slice, or
	// mutating one rewrites the other. The existing fields-map test does not
	// reach inside the list.
	f := FileEvent{Path: "a.go", NewMarkers: []Marker{{Kind: "k", FQN: "f", Line: 1}}}
	first := f.Event(KindPreCreate).Fields[FieldNewMarkers].([]any)
	second := f.Event(KindPreCreate).Fields[FieldNewMarkers].([]any)

	first[0].(map[string]any)[KeyMarkerFQN] = "mutated"
	assert.Equal(t, "f", second[0].(map[string]any)[KeyMarkerFQN])
}
