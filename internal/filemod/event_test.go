package filemod

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
)

func TestFileEvent_Event_PathOnly(t *testing.T) {
	e := FileEvent{Path: "memories/a.md"}.Event(KindPreUpdate)

	assert.Equal(t, KindPreUpdate, e.Kind)
	assert.Equal(t, map[string]any{
		FieldPath: "memories/a.md",
		// Present and empty. PreFileUpdate declares markers, so it carries
		// them; this FileEvent simply has none.
		FieldMarkers: []any{},
		// Both present, because PreFileUpdate declares both and presence is
		// decided by the declaration rather than by the value. A bare
		// FileEvent has derived nothing, so resultKnown is false and result
		// holds its zero value — which is exactly the pair a rule must consult
		// together.
		FieldResult:      "",
		FieldResultKnown: false,
	}, e.Fields)
	assert.NotContains(t, e.Fields, FieldContent,
		"content is absent, not empty: on every kind but PreFileCreate the file is on disk")
}

func TestFileEvent_Event_WithContent(t *testing.T) {
	e := FileEvent{Path: "memories/a.md", Content: "# Notes\n"}.Event(KindPreCreate)

	assert.Equal(t, KindPreCreate, e.Kind)
	assert.Equal(t, map[string]any{
		FieldPath:    "memories/a.md",
		FieldContent: "# Notes\n",
		FieldMarkers: []any{},
	}, e.Fields)
}

// TestFileEvent_Event_EmptyContentIsCarried states what is now true: content is
// carried whenever the kind declares it, empty or not, so a file whose content
// genuinely is the empty string produces an event with `content: ""` — and
// `content == ""`, the rule an author writes to catch an empty file, fires.
//
// This test previously asserted the opposite and pinned the defect. Content and
// markers are now carried by the same rule: the declaration decides presence,
// the value decides only what is held. `len(markers) == 0` and `content == ""`
// are both real questions an author asks, and neither may error.
func TestFileEvent_Event_EmptyContentIsCarried(t *testing.T) {
	e := FileEvent{Path: "empty.md", Content: ""}.Event(KindPreCreate)

	require.Contains(t, e.Fields, FieldContent)
	assert.Equal(t, "", e.Fields[FieldContent])
	// path, content and markers — every field PreFileCreate declares.
	assert.Len(t, e.Fields, 3)
	assert.Contains(t, e.Fields, FieldMarkers)
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
	e := FileEvent{Path: "a.md", Content: "x"}.Event("NotAFileKind")
	require.Contains(t, e.Fields, FieldPath, "an event must always name its file")
	assert.Equal(t, "a.md", e.Fields[FieldPath])
	assert.NotContains(t, e.Fields, FieldContent, "an unknown kind declares nothing else")
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
	f := FileEvent{Path: "a.md", Content: "x"}
	first := f.Event(KindPreCreate)
	second := f.Event(KindPreCreate)

	first.Fields[FieldPath] = "mutated"
	assert.Equal(t, "a.md", second.Fields[FieldPath])
}

func TestFromEvent_RoundTrip(t *testing.T) {
	for name, in := range map[string]FileEvent{
		"path only":        {Path: "memories/a.md"},
		"path and content": {Path: "memories/a.md", Content: "# Notes\n"},
		"unicode path":     {Path: "памʼять/файл.md", Content: "Правило ✅\n"},
		"content with nul": {Path: "a.bin", Content: "a\x00b"},
		"whitespace body":  {Path: "a.md", Content: "  \n\t\n"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := FromEvent(in.Event(KindPreCreate))
			require.NoError(t, err)

			// Markers do not round-trip nil: a FileEvent built without any
			// comes back with an EMPTY list, because the wire form carries the
			// field present-and-empty on every kind that declares it. That
			// asymmetry is the point — `len(markers) == 0` must hold for a file
			// with no markers rather than error on an absent field — so it is
			// asserted rather than normalised away.
			assert.NotNil(t, got.Markers, "the wire form is empty, not absent")
			assert.Empty(t, got.Markers)

			got.Markers = in.Markers
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
	assert.Empty(t, got.Content)
}

func TestFromEvent_NoPathIsAnError(t *testing.T) {
	// Silently returning a zero value would let a caller act on a file that
	// was never named.
	cases := map[string]event.Event{
		"nil fields":   {Kind: KindPreCreate},
		"empty fields": {Kind: KindPreCreate, Fields: map[string]any{}},
		"empty path":   {Kind: KindPreCreate, Fields: map[string]any{FieldPath: ""}},
		"content only": {Kind: KindPreCreate, Fields: map[string]any{FieldContent: "x"}},
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
			FieldPath:    "a.md",
			FieldContent: 42,
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

func TestModule_ContentOnPreCreateAlone(t *testing.T) {
	// The file does not exist yet only on PreFileCreate, so that is the one
	// kind with nowhere else for a rule to look.
	fieldsOf := map[string][]string{}
	for _, k := range New().Kinds() {
		for _, f := range k.Fields {
			fieldsOf[k.Name] = append(fieldsOf[k.Name], f.Name)
		}
	}

	assert.Equal(t, []string{FieldPath, FieldContent, FieldMarkers}, fieldsOf[KindPreCreate])
	for _, kind := range []string{
		KindPreDelete, KindPostCreate, KindPostUpdate, KindPostDelete,
	} {
		assert.Equal(t, []string{FieldPath}, fieldsOf[kind], "kind %q", kind)
	}
	assert.Equal(t, []string{FieldPath, FieldResult, FieldResultKnown, FieldMarkers},
		fieldsOf[KindPreUpdate],
		"result and resultKnown but still no content: `content` stays the "+
			"create-only field meaning \"the body this action states outright\", "+
			"while the post-edit bytes an update computes get their own name")
}

func TestModule_MarkersOnTheTwoKindsWithText(t *testing.T) {
	// Create and update have text; a delete does not, and an always-empty
	// field is one a rule can match on and never learn from. The Post kinds
	// are diff observations and carry the path alone.
	declares := map[string]bool{}
	for _, k := range New().Kinds() {
		for _, f := range k.Fields {
			if f.Name == FieldMarkers {
				declares[k.Name] = true
			}
		}
	}
	assert.Equal(t, map[string]bool{KindPreCreate: true, KindPreUpdate: true}, declares)
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

func TestModule_MarkersDeclaresItsElementShape(t *testing.T) {
	// The whole point of Elem. A list whose Elem is nil has its collection
	// checked and its predicate body left unchecked, so a typo INSIDE
	// `any(markers, .knid == "docs")` would compile, load, and never fire.
	// See TestCompileMatcherFor_MarkerPredicateTypo in internal/guardrail for
	// the end-to-end proof that the refusal actually happens.
	var markers *module.FieldDecl
	for _, k := range New().Kinds() {
		for i, f := range k.Fields {
			if f.Name == FieldMarkers {
				markers = &k.Fields[i]
			}
		}
	}
	require.NotNil(t, markers)
	require.Equal(t, module.TypeList, markers.Type)
	require.NotNil(t, markers.Elem, "a nil Elem leaves the predicate body unchecked")
	require.Equal(t, module.TypeMap, markers.Elem.Type,
		"only a TypeMap with Fields resolves to a closed structure in matcherenv")

	byName := map[string]module.FieldType{}
	for _, f := range markers.Elem.Fields {
		byName[f.Name] = f.Type
	}
	assert.Equal(t, map[string]module.FieldType{
		KeyMarkerKind: module.TypeString,
		KeyMarkerFQN:  module.TypeString,
		KeyMarkerLine: module.TypeInt,
	}, byName)
}

func TestModule_KindsIsStable(t *testing.T) {
	assert.Equal(t, New().Kinds(), New().Kinds())
}

func TestFileEvent_MarkersAreCarriedExactlyWhereDeclared(t *testing.T) {
	// The claim in Event's doc comment: markers are keyed off the declaration,
	// so the wire form cannot carry them on a kind that does not declare them or
	// omit them on one that does. Checked against every kind rather than the two
	// that were on my mind, and with a FileEvent that HOLDS markers, so a kind
	// that leaked them would be caught rather than passing on an empty struct.
	f := FileEvent{Path: "a.go", Markers: []Marker{{Kind: "k", FQN: "f", Line: 1}}}
	for _, k := range New().Kinds() {
		declared := false
		for _, fd := range k.Fields {
			if fd.Name == FieldMarkers {
				declared = true
			}
		}
		fields := f.Event(k.Name).Fields
		if declared {
			assert.Containsf(t, fields, FieldMarkers, "kind %q declares markers but does not carry them", k.Name)
			continue
		}
		assert.NotContainsf(t, fields, FieldMarkers, "kind %q carries markers it does not declare", k.Name)
	}
}

// TestFileEvent_ContentIsOmittedFromKindsThatDoNotDeclareIt is the fixed form
// of a test that used to pin the opposite.
//
// Event once set content whenever FileEvent.Content was non-empty without
// asking whether the kind declared it, so a FileEvent carrying content produced
// a PreFileDelete with a content field no matcher could be checked against —
// CompileMatcherFor validates names against the declaration and refuses one
// that is not there. Every field now goes through kindDeclares, so presence is
// the declaration's answer and never the value's.
func TestFileEvent_ContentIsOmittedFromKindsThatDoNotDeclareIt(t *testing.T) {
	f := FileEvent{Path: "a.go", Content: "x"}
	for _, kind := range []string{
		KindPreDelete, KindPostCreate, KindPostUpdate, KindPostDelete,
	} {
		assert.NotContainsf(t, f.Event(kind).Fields, FieldContent,
			"kind %q does not declare content and must not carry it", kind)
	}
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
// empty file. `content` is required on PreFileCreate (spec events/main.tsp),
// and a matcher written `content == ""` — the rule an author writes to catch an
// empty file — is the one that met a nil and errored.
func TestFileEvent_EmptyContentIsCarriedOnPreFileCreate(t *testing.T) {
	fields := FileEvent{Path: "empty.txt", Content: ""}.Event(KindPreCreate).Fields
	require.Contains(t, fields, FieldContent,
		"a genuinely empty file must still carry the content its kind declares")
	assert.Equal(t, "", fields[FieldContent])
}

func TestFileEvent_EveryKindsMarkersAreFreshPerCall(t *testing.T) {
	// Two events from one FileEvent must not share the markers slice, or
	// mutating one rewrites the other. The existing fields-map test does not
	// reach inside the list.
	f := FileEvent{Path: "a.go", Markers: []Marker{{Kind: "k", FQN: "f", Line: 1}}}
	first := f.Event(KindPreCreate).Fields[FieldMarkers].([]any)
	second := f.Event(KindPreCreate).Fields[FieldMarkers].([]any)

	first[0].(map[string]any)[KeyMarkerFQN] = "mutated"
	assert.Equal(t, "f", second[0].(map[string]any)[KeyMarkerFQN])
}
