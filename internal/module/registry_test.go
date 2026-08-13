package module

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
)

// fakeModule is a module built inline: a name, the kinds it claims, and events
// it hands back. Enough to exercise the registry, which never runs a module's
// logic — only reads what it declares.
type fakeModule struct {
	name  string
	kinds []KindDecl
	out   []event.Event
	err   error
}

func (f *fakeModule) Name() string      { return f.name }
func (f *fakeModule) Kinds() []KindDecl { return f.kinds }
func (f *fakeModule) Extract(Input) ([]event.Event, error) {
	return f.out, f.err
}

// mod is shorthand for a module claiming the given kinds, each with no fields.
func mod(name string, kinds ...string) *fakeModule {
	decls := make([]KindDecl, 0, len(kinds))
	for _, k := range kinds {
		decls = append(decls, KindDecl{Name: k})
	}
	return &fakeModule{name: name, kinds: decls}
}

func TestNewRegistry_Empty(t *testing.T) {
	r, err := NewRegistry()
	require.NoError(t, err)
	require.NotNil(t, r)
	assert.Empty(t, r.DeclaredKinds())
	assert.Empty(t, r.Needed(nil))
}

func TestNewRegistry_HappyPath(t *testing.T) {
	file := mod("file", "PreFileCreate", "PostFileCreate")
	command := mod("command", "PreCommand")

	r, err := NewRegistry(file, command)
	require.NoError(t, err)

	owner, ok := r.Lookup("PreFileCreate")
	require.True(t, ok)
	assert.Same(t, file, owner)

	owner, ok = r.Lookup("PreCommand")
	require.True(t, ok)
	assert.Same(t, command, owner)

	assert.Equal(t,
		[]string{"PostFileCreate", "PreCommand", "PreFileCreate"},
		r.DeclaredKinds(),
		"DeclaredKinds is sorted, so the hint given to a rule author is stable")
}

func TestNewRegistry_TwoModulesClaimingOneKindIsRefused(t *testing.T) {
	// Caught at registration, not left to surface as whichever module was
	// asked first: an event two modules answer to is one nobody can attribute.
	first := mod("file", "PreFileCreate")
	second := mod("shadow", "PreFileCreate")

	r, err := NewRegistry(first, second)
	require.Error(t, err)
	assert.Nil(t, r, "a colliding registry is not returned half-built")
	assert.Contains(t, err.Error(), `module "shadow"`)
	assert.Contains(t, err.Error(), `kind "PreFileCreate"`)
	assert.Contains(t, err.Error(), `already declared by "file"`)
}

func TestNewRegistry_SelfCollisionWithinOneModule(t *testing.T) {
	// A module declaring the same kind twice collides with itself.
	r, err := NewRegistry(mod("file", "PreFileCreate", "PreFileCreate"))
	require.Error(t, err)
	assert.Nil(t, r)
	assert.Contains(t, err.Error(), "already declared")
}

func TestNewRegistry_ModuleWithNoNameIsRefused(t *testing.T) {
	r, err := NewRegistry(mod("", "SomeKind"))
	require.Error(t, err)
	assert.Nil(t, r)
	assert.Contains(t, err.Error(), "registered with no name")
}

func TestNewRegistry_DuplicateNameIsRefused(t *testing.T) {
	r, err := NewRegistry(mod("file", "A"), mod("file", "B"))
	require.Error(t, err)
	assert.Nil(t, r)
	assert.Contains(t, err.Error(), `module "file": already registered`)
}

func TestNewRegistry_KindWithNoNameIsRefused(t *testing.T) {
	r, err := NewRegistry(mod("file", ""))
	require.Error(t, err)
	assert.Nil(t, r)
	assert.Contains(t, err.Error(), "declared a kind with no name")
}

// TestNewRegistry_AddIsNotAtomic DOCUMENTS A KNOWN DEFECT.
//
// This test asserts what the code does today, NOT what it should do. When the
// defect is fixed, this test MUST be changed — its failure is the expected
// consequence of the fix, not a regression. Do not "repair" it by reverting
// the fix.
//
// The defect: add() writes into r.owner and r.decl as it walks a module's
// kinds (registry.go:60-61), so a module declaring a valid kind and then an
// invalid one has already claimed the valid kind by the time it returns an
// error. NewRegistry discards the whole registry on error, so nothing
// observable escapes today — but add() is a method on a live registry, and any
// future caller registering into one incrementally inherits a module that owns
// kinds while not being registered under its own name.
//
// Corrected behaviour would be: add() leaves the registry exactly as it found
// it when it fails — staging the kinds and committing them only once every
// kind has been validated. The assertion below on ownsGood would then flip
// from True to False, and the assertion on `registered` would stay False.
func TestNewRegistry_AddIsNotAtomic(t *testing.T) {
	r := &Registry{
		byName: map[string]Module{},
		owner:  map[string]Module{},
		decl:   map[string]KindDecl{},
	}
	m := mod("partial", "GoodKind", "")

	err := r.add(m)
	require.Error(t, err)

	_, ownsGood := r.Lookup("GoodKind")
	assert.True(t, ownsGood,
		"DEFECT: the kind declared before the failure is already claimed. "+
			"Flip to assert.False once add() rolls back on error.")
	_, registered := r.byName["partial"]
	assert.False(t, registered, "but the module itself was never recorded under its name")
}

func TestLookup_KindNobodyOwns(t *testing.T) {
	r, err := NewRegistry(mod("file", "PreFileCreate"))
	require.NoError(t, err)

	for _, kind := range []string{"NoSuchKind", "", "prefilecreate", "PreFileCreate "} {
		owner, ok := r.Lookup(kind)
		assert.False(t, ok, "kind %q must be unowned", kind)
		assert.Nil(t, owner)
	}
}

func TestKindDeclFor(t *testing.T) {
	path := FieldDecl{Name: "path", Type: TypeString}
	content := FieldDecl{Name: "content", Type: TypeString}
	file := &fakeModule{name: "file", kinds: []KindDecl{
		{Name: "PreFileCreate", Fields: []FieldDecl{path, content}},
		{Name: "PreFileUpdate", Fields: []FieldDecl{path}},
	}}

	r, err := NewRegistry(file)
	require.NoError(t, err)

	decl, ok := r.KindDeclFor("PreFileCreate")
	require.True(t, ok)
	assert.Equal(t, "PreFileCreate", decl.Name)
	assert.Equal(t, []FieldDecl{path, content}, decl.Fields,
		"the fields a matcher may be checked against")

	decl, ok = r.KindDeclFor("PreFileUpdate")
	require.True(t, ok)
	assert.Equal(t, []FieldDecl{path}, decl.Fields,
		"content is carried by PreFileCreate alone")
}

func TestKindDeclFor_KindNobodyOwns(t *testing.T) {
	r, err := NewRegistry(mod("file", "PreFileCreate"))
	require.NoError(t, err)

	decl, ok := r.KindDeclFor("NoSuchKind")
	assert.False(t, ok)
	assert.Equal(t, KindDecl{}, decl)

	decl, ok = r.KindDeclFor("")
	assert.False(t, ok)
	assert.Equal(t, KindDecl{}, decl)
}

func TestDeclaredKinds_SortedAndComplete(t *testing.T) {
	r, err := NewRegistry(
		mod("zeta", "Zed", "Alpha"),
		mod("alpha", "Mid"),
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"Alpha", "Mid", "Zed"}, r.DeclaredKinds())
}

func TestNeeded_OnlyWhatSomeBindingAsksFor(t *testing.T) {
	// An extractor runs only when some binding names a kind it produces:
	// a project with no rule about commands pays nothing for command parsing.
	file := mod("file", "PreFileCreate", "PostFileCreate")
	command := mod("command", "PreCommand")
	marker := mod("marker", "TurnEnd")

	r, err := NewRegistry(file, command, marker)
	require.NoError(t, err)

	needed := r.Needed([]string{"PreFileCreate"})
	require.Len(t, needed, 1)
	assert.Same(t, file, needed[0])

	needed = r.Needed([]string{"PreCommand", "TurnEnd"})
	require.Len(t, needed, 2)
	assert.Equal(t, []string{"command", "marker"}, names(needed))
}

func TestNeeded_NoBindingsNeedsNoModules(t *testing.T) {
	r, err := NewRegistry(mod("file", "PreFileCreate"), mod("command", "PreCommand"))
	require.NoError(t, err)

	assert.Empty(t, r.Needed(nil))
	assert.Empty(t, r.Needed([]string{}))
}

func TestNeeded_UnknownKindsAreIgnored(t *testing.T) {
	// A declaration naming a kind that will never arrive does not make a
	// module needed, and does not fail here — Needed is a selection, not a
	// validation.
	r, err := NewRegistry(mod("file", "PreFileCreate"))
	require.NoError(t, err)

	assert.Empty(t, r.Needed([]string{"NoSuchKind", "AlsoMissing"}))

	needed := r.Needed([]string{"NoSuchKind", "PreFileCreate"})
	require.Len(t, needed, 1)
	assert.Equal(t, "file", needed[0].Name())
}

func TestNeeded_DeduplicatesAcrossKinds(t *testing.T) {
	// Two bindings on two kinds of one module still need that module once.
	file := mod("file", "PreFileCreate", "PostFileCreate", "PreFileDelete")
	r, err := NewRegistry(file)
	require.NoError(t, err)

	needed := r.Needed([]string{
		"PreFileCreate", "PostFileCreate", "PreFileCreate", "PreFileDelete",
	})
	require.Len(t, needed, 1)
	assert.Same(t, file, needed[0])
}

func TestNeeded_StableOrderRegardlessOfInputOrder(t *testing.T) {
	// Sorted by module name, so the same set of bindings always produces the
	// same extractor order however the kinds were listed.
	r, err := NewRegistry(
		mod("zulu", "Z1"),
		mod("alpha", "A1"),
		mod("mike", "M1"),
	)
	require.NoError(t, err)

	want := []string{"alpha", "mike", "zulu"}
	for _, kinds := range [][]string{
		{"Z1", "A1", "M1"},
		{"A1", "M1", "Z1"},
		{"M1", "Z1", "A1"},
		{"Z1", "M1", "A1", "Z1"},
	} {
		assert.Equal(t, want, names(r.Needed(kinds)), "kinds %v", kinds)
	}
}

func names(mods []Module) []string {
	out := make([]string, 0, len(mods))
	for _, m := range mods {
		out = append(out, m.Name())
	}
	return out
}
