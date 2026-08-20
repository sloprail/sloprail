package module

import (
	"sort"
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
	r, err := NewRegistryForTest()
	require.NoError(t, err)
	require.NotNil(t, r)
	assert.Empty(t, r.DeclaredKinds())
	assert.Empty(t, r.Needed(nil))
}

func TestNewRegistry_HappyPath(t *testing.T) {
	file := mod("file", "PreFileCreate", "PostFileCreate")
	command := mod("command", "PreCommand")

	r, err := NewRegistryForTest(file, command)
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

	r, err := NewRegistryForTest(first, second)
	require.Error(t, err)
	assert.Nil(t, r, "a colliding registry is not returned half-built")
	assert.Contains(t, err.Error(), `module "shadow"`)
	assert.Contains(t, err.Error(), `kind "PreFileCreate"`)
	assert.Contains(t, err.Error(), `already declared by "file"`)
}

func TestNewRegistry_SelfCollisionWithinOneModule(t *testing.T) {
	// A module declaring the same kind twice is refused, and told which kind
	// rather than that it collided "with file" — naming the module against
	// itself was an artefact of the check reading a write the same loop had
	// just made, and it read as though a second module were involved.
	r, err := NewRegistryForTest(mod("file", "PreFileCreate", "PreFileCreate"))
	require.Error(t, err)
	assert.Nil(t, r)
	assert.Contains(t, err.Error(), `module "file"`)
	assert.Contains(t, err.Error(), `kind "PreFileCreate"`)
	assert.Contains(t, err.Error(), "twice")
}

func TestNewRegistry_ModuleWithNoNameIsRefused(t *testing.T) {
	r, err := NewRegistryForTest(mod("", "SomeKind"))
	require.Error(t, err)
	assert.Nil(t, r)
	assert.Contains(t, err.Error(), "registered with no name")
}

func TestNewRegistry_DuplicateNameIsRefused(t *testing.T) {
	r, err := NewRegistryForTest(mod("file", "A"), mod("file", "B"))
	require.Error(t, err)
	assert.Nil(t, r)
	assert.Contains(t, err.Error(), `module "file": already registered`)
}

func TestNewRegistry_KindWithNoNameIsRefused(t *testing.T) {
	r, err := NewRegistryForTest(mod("file", ""))
	require.Error(t, err)
	assert.Nil(t, r)
	assert.Contains(t, err.Error(), "declared a kind with no name")
}

// TestAdd_FailureLeavesTheRegistryUntouched is the invariant that replaced a
// documented defect: add() either registers a module completely or changes
// nothing.
//
// It used to write each kind into r.owner as it walked, so a module declaring a
// valid kind and then an invalid one left the valid kind claimed by a module
// that was never recorded under its own name. NewRegistry hides that by
// discarding the registry on error — this exercises add() directly, on a live
// registry, which is where the half-written state was actually reachable.
func TestAdd_FailureLeavesTheRegistryUntouched(t *testing.T) {
	for _, tc := range []struct {
		name   string
		module Module
		reason string
	}{
		{"a kind with no name", mod("partial", "GoodKind", ""), "declared a kind with no name"},
		{"a kind another module owns", mod("partial", "GoodKind", "Taken"), "already declared by"},
		{"the same kind twice", mod("partial", "GoodKind", "GoodKind"), "twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// An incumbent, so the collision case has something to collide
			// with and so the test can prove nothing already there was
			// disturbed either.
			r, err := NewRegistryForTest(mod("incumbent", "Taken"))
			require.NoError(t, err)

			before := snapshot(r)

			err = r.add(tc.module)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.reason)

			assert.Equal(t, before, snapshot(r),
				"a failed add must leave the registry exactly as it found it")

			// Spelled out as well as compared, so a failure names the thing
			// that leaked rather than printing two maps.
			_, ownsGood := r.Lookup("GoodKind")
			assert.False(t, ownsGood, "the kind declared before the failure must not be claimed")
			_, hasDecl := r.KindDeclFor("GoodKind")
			assert.False(t, hasDecl, "nor may its declaration be recorded")
			_, registered := r.byName["partial"]
			assert.False(t, registered, "and the module itself is not registered")
		})
	}
}

// TestAdd_SucceedsWholly is the other half: when add() reports success, every
// kind is claimed and the module is registered. Without it the invariant above
// is satisfied by an add() that never writes anything at all.
func TestAdd_SucceedsWholly(t *testing.T) {
	r, err := NewRegistryForTest(mod("incumbent", "Taken"))
	require.NoError(t, err)

	require.NoError(t, r.add(mod("late", "One", "Two")))

	for _, kind := range []string{"One", "Two"} {
		owner, ok := r.Lookup(kind)
		require.True(t, ok, "kind %q must be claimed", kind)
		assert.Equal(t, "late", owner.Name())
		_, hasDecl := r.KindDeclFor(kind)
		assert.True(t, hasDecl, "kind %q must have its declaration recorded", kind)
	}
	assert.Equal(t, []string{"incumbent", "late"}, r.ModuleNames())
}

// snapshot is every mapping the registry holds, in a form two of which can be
// compared. Comparing the maps directly is what makes the assertion "nothing
// changed" rather than "these particular things I remembered to check did not".
func snapshot(r *Registry) map[string][]string {
	s := map[string][]string{}
	for kind, m := range r.owner {
		s["owner"] = append(s["owner"], kind+"="+m.Name())
	}
	for kind := range r.decl {
		s["decl"] = append(s["decl"], kind)
	}
	s["byName"] = r.ModuleNames()
	for _, v := range s {
		sort.Strings(v)
	}
	return s
}

func TestLookup_KindNobodyOwns(t *testing.T) {
	r, err := NewRegistryForTest(mod("file", "PreFileCreate"))
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

	r, err := NewRegistryForTest(file)
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
	r, err := NewRegistryForTest(mod("file", "PreFileCreate"))
	require.NoError(t, err)

	decl, ok := r.KindDeclFor("NoSuchKind")
	assert.False(t, ok)
	assert.Equal(t, KindDecl{}, decl)

	decl, ok = r.KindDeclFor("")
	assert.False(t, ok)
	assert.Equal(t, KindDecl{}, decl)
}

func TestDeclaredKinds_SortedAndComplete(t *testing.T) {
	r, err := NewRegistryForTest(
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
	marker := mod("marker", "Stop")

	r, err := NewRegistryForTest(file, command, marker)
	require.NoError(t, err)

	needed := r.Needed([]string{"PreFileCreate"})
	require.Len(t, needed, 1)
	assert.Same(t, file, needed[0])

	needed = r.Needed([]string{"PreCommand", "Stop"})
	require.Len(t, needed, 2)
	assert.Equal(t, []string{"command", "marker"}, names(needed))
}

func TestNeeded_NoBindingsNeedsNoModules(t *testing.T) {
	r, err := NewRegistryForTest(mod("file", "PreFileCreate"), mod("command", "PreCommand"))
	require.NoError(t, err)

	assert.Empty(t, r.Needed(nil))
	assert.Empty(t, r.Needed([]string{}))
}

func TestNeeded_UnknownKindsAreIgnored(t *testing.T) {
	// A declaration naming a kind that will never arrive does not make a
	// module needed, and does not fail here — Needed is a selection, not a
	// validation.
	r, err := NewRegistryForTest(mod("file", "PreFileCreate"))
	require.NoError(t, err)

	assert.Empty(t, r.Needed([]string{"NoSuchKind", "AlsoMissing"}))

	needed := r.Needed([]string{"NoSuchKind", "PreFileCreate"})
	require.Len(t, needed, 1)
	assert.Equal(t, "file", needed[0].Name())
}

func TestNeeded_DeduplicatesAcrossKinds(t *testing.T) {
	// Two bindings on two kinds of one module still need that module once.
	file := mod("file", "PreFileCreate", "PostFileCreate", "PreFileDelete")
	r, err := NewRegistryForTest(file)
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
	r, err := NewRegistryForTest(
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
