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
	assert.Equal(t, map[string]any{FieldPath: "memories/a.md"}, e.Fields)
	assert.NotContains(t, e.Fields, FieldContent,
		"content is absent, not empty: on every kind but PreFileCreate the file is on disk")
}

func TestFileEvent_Event_WithContent(t *testing.T) {
	e := FileEvent{Path: "memories/a.md", Content: "# Notes\n"}.Event(KindPreCreate)

	assert.Equal(t, KindPreCreate, e.Kind)
	assert.Equal(t, map[string]any{
		FieldPath:    "memories/a.md",
		FieldContent: "# Notes\n",
	}, e.Fields)
}

// TestFileEvent_Event_EmptyContentIsOmitted pins CURRENT behaviour: the field
// is set only when non-empty, so a file whose content genuinely is the empty
// string produces an event with no content field at all. A matcher written as
// `content == ""` therefore does not fire for a truly empty file — it reads a
// nil, which is not equal to "".
func TestFileEvent_Event_EmptyContentIsOmitted(t *testing.T) {
	e := FileEvent{Path: "empty.md", Content: ""}.Event(KindPreCreate)

	assert.NotContains(t, e.Fields, FieldContent)
	assert.Len(t, e.Fields, 1)
}

func TestFileEvent_Event_KindIsPassedThroughUnchecked(t *testing.T) {
	// Event does not police the kind; the caller picks it, and the registry is
	// what decides whether a kind is one this module owns.
	e := FileEvent{Path: "a.md"}.Event("NotAFileKind")
	assert.Equal(t, "NotAFileKind", e.Kind)
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
			assert.Equal(t, in, got)
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

	assert.Equal(t, []string{FieldPath, FieldContent}, fieldsOf[KindPreCreate])
	for _, kind := range []string{
		KindPreUpdate, KindPreDelete, KindPostCreate, KindPostUpdate, KindPostDelete,
	} {
		assert.Equal(t, []string{FieldPath}, fieldsOf[kind], "kind %q", kind)
	}
}

func TestModule_EveryDeclaredFieldIsTyped(t *testing.T) {
	for _, k := range New().Kinds() {
		for _, f := range k.Fields {
			assert.Equal(t, module.TypeString, f.Type,
				"kind %q field %q", k.Name, f.Name)
		}
	}
}

func TestModule_KindsIsStable(t *testing.T) {
	assert.Equal(t, New().Kinds(), New().Kinds())
}
