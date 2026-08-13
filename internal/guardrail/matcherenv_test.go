package guardrail

import (
	"testing"

	"github.com/expr-lang/expr/types"
	"github.com/stretchr/testify/assert"

	"github.com/sloprail/sloprail/internal/module"
)

func TestMatcherEnv_CarriesEveryDeclaredField(t *testing.T) {
	env := matcherEnv(fileKind)

	assert.Len(t, env, 2)
	assert.Contains(t, env, "path")
	assert.Contains(t, env, "content")
}

// An undeclared name must be absent, since absence is what makes the checker
// refuse the matcher that reads it.
func TestMatcherEnv_OmitsWhatWasNotDeclared(t *testing.T) {
	k := module.KindDecl{Name: "PreFileUpdate", Fields: []module.FieldDecl{{Name: "path", Type: module.TypeString}}}

	assert.NotContains(t, matcherEnv(k), "content")
}

func TestMatcherEnv_KindWithNoFields(t *testing.T) {
	assert.Empty(t, matcherEnv(module.KindDecl{Name: "Something"}))
}

func TestFieldType_Scalars(t *testing.T) {
	assert.Equal(t, types.String, fieldType(module.FieldDecl{Type: module.TypeString}))
	assert.Equal(t, types.Bool, fieldType(module.FieldDecl{Type: module.TypeBool}))
}

// A list without a declared element shape stays open at the element.
func TestFieldType_ListWithoutElem(t *testing.T) {
	assert.Equal(t, types.Array(types.Any), fieldType(module.FieldDecl{Type: module.TypeList}))
}

// A list with one is typed all the way down, which is what makes a predicate
// body checkable.
func TestFieldType_ListWithElem(t *testing.T) {
	f := module.FieldDecl{
		Type: module.TypeList,
		Elem: &module.FieldDecl{Type: module.TypeString},
	}

	assert.Equal(t, types.Array(types.String), fieldType(f))
}

// A map whose keys the module never claimed to know stays open. A closed type
// would refuse `meta.whatever` on a declared field.
func TestFieldType_MapWithoutFieldsIsOpen(t *testing.T) {
	assert.Equal(t, types.Any, fieldType(module.FieldDecl{Type: module.TypeMap}))
}

// Enumerating the keys is the module asserting these are the keys, which is
// what makes refusing the others fair.
func TestFieldType_MapWithFieldsIsClosed(t *testing.T) {
	f := module.FieldDecl{
		Type:   module.TypeMap,
		Fields: []module.FieldDecl{{Name: "bin", Type: module.TypeString}},
	}

	assert.Equal(t, types.Map{"bin": types.String}, fieldType(f))
}

// A type this build does not recognise is a reason to check less, never a
// reason to refuse a matcher for a fault that is ours.
func TestFieldType_UnknownFallsBackToAny(t *testing.T) {
	assert.Equal(t, types.Any, fieldType(module.FieldDecl{Type: module.FieldType("quaternion")}))
}
