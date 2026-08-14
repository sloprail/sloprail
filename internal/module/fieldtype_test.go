package module

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
)

// typeModule declares one kind with whatever field shape a test hands it.
type typeModule struct {
	name   string
	fields []FieldDecl
}

func (m typeModule) Name() string { return m.name }
func (m typeModule) Kinds() []KindDecl {
	return []KindDecl{{Name: "K", Fields: m.fields}}
}
func (m typeModule) Extract(Input) ([]event.Event, error) { return nil, nil }

// A field type this build does not understand is refused at registration, by
// name, rather than silently checked as Any.
//
// The defect it closes is the one TypeInt was: an unknown type reaches three
// switches that each answer "nothing declared", and together they fail open on
// a carried value and refuse spuriously on an absent one. "integer" is the
// spelling that makes it concrete — a plausible typo for the newest constant.
func TestRegistry_RefusesUnknownFieldType(t *testing.T) {
	_, err := NewRegistryForTest(typeModule{
		name:   "m",
		fields: []FieldDecl{{Name: "line", Type: FieldType("integer")}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line")
	assert.Contains(t, err.Error(), "integer")
	// It must name the alternatives, or the maintainer's next move is a guess.
	for _, known := range FieldTypes() {
		assert.Contains(t, err.Error(), string(known))
	}
}

// The nested spellings, which are the ones that hide: a list's element and a
// map's key reach the same three switches one level down, where the collection
// checks clean and the element silently does not.
func TestRegistry_RefusesUnknownFieldTypeNested(t *testing.T) {
	cases := []struct {
		name  string
		field FieldDecl
		want  string
	}{
		{
			name: "list element",
			field: FieldDecl{Name: "marks", Type: TypeList,
				Elem: &FieldDecl{Type: TypeMap, Fields: []FieldDecl{
					{Name: "line", Type: FieldType("integer")},
				}}},
			want: "line",
		},
		{
			name: "map key",
			field: FieldDecl{Name: "meta", Type: TypeMap, Fields: []FieldDecl{
				{Name: "count", Type: FieldType("number")},
			}},
			want: "count",
		},
		{
			name:  "bare list element",
			field: FieldDecl{Name: "xs", Type: TypeList, Elem: &FieldDecl{Type: FieldType("str")}},
			want:  "str",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewRegistryForTest(typeModule{name: "m", fields: []FieldDecl{tc.field}})
			require.Error(t, err, "an unknown type nested inside %s must be refused", tc.name)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// The empty type is the absent one — a FieldDecl written without a Type at all.
// It is unknown like any other, and must not pass for "the module said nothing,
// so check nothing": the module DID declare a field, and a field the engine
// cannot check is the silence this refusal exists to break.
func TestRegistry_RefusesEmptyFieldType(t *testing.T) {
	_, err := NewRegistryForTest(typeModule{
		name:   "m",
		fields: []FieldDecl{{Name: "path"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path")
}

// Every declared type registers. This is the half that fails when a sixth type
// is added to the constant block and not to knownFieldTypes — the enumeration
// that reads as exhaustive and is one short.
func TestRegistry_AcceptsEveryKnownFieldType(t *testing.T) {
	for _, ft := range FieldTypes() {
		_, err := NewRegistryForTest(typeModule{
			name:   "m",
			fields: []FieldDecl{{Name: "f", Type: ft}},
		})
		assert.NoError(t, err, "declared type %q must register", ft)
	}
}

// A refused module leaves the registry untouched — the atomicity `add` already
// promises, now that a new way to fail runs before the write.
func TestRegistry_UnknownFieldTypeLeavesNothingRegistered(t *testing.T) {
	r, err := NewRegistryForTest()
	require.NoError(t, err)
	err = r.add(typeModule{name: "m", fields: []FieldDecl{{Name: "f", Type: FieldType("bogus")}}})
	require.Error(t, err)
	assert.Empty(t, r.ModuleNames(), "a module refused for a bad field type must not be registered")
	assert.Empty(t, r.DeclaredKinds(), "nor may its kinds be")
}

// FieldTypes covers the constant block. A type added to the block and not to
// knownFieldTypes would make every real module carrying it fail to register,
// which is loud — this asserts the list is the one the errors quote.
func TestFieldTypes_IsSortedAndComplete(t *testing.T) {
	got := FieldTypes()
	assert.Equal(t, len(knownFieldTypes), len(got))
	assert.True(t, sortedStrings(got), "FieldTypes must be stable for error text: %v", got)
	for _, ft := range []FieldType{TypeString, TypeBool, TypeInt, TypeList, TypeMap} {
		assert.True(t, KnownFieldType(ft), "%q is declared and must be known", ft)
	}
	assert.False(t, KnownFieldType(FieldType("quaternion")))
	assert.False(t, KnownFieldType(""))
	assert.NotContains(t, strings.Join(asStrings(got), ","), "quaternion")
}

func sortedStrings(ts []FieldType) bool {
	for i := 1; i < len(ts); i++ {
		if ts[i-1] > ts[i] {
			return false
		}
	}
	return true
}

func asStrings(ts []FieldType) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = string(t)
	}
	return out
}
