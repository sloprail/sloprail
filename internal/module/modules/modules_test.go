package modules

import (
	"go/importer"
	"go/token"
	"go/types"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/module"
)

// modulePkgPath is the package declaring the Module interface — the definition
// of "is a module" that discovery below is keyed on.
const modulePkgPath = "github.com/sloprail/sloprail/internal/module"

func TestAll_IsNotEmpty(t *testing.T) {
	assert.NotEmpty(t, All(), "a build with no modules can produce no events, so every guardrail in every project binds to nothing")
}

// TestRegistry_RegistersCleanly catches at test time what would otherwise be a
// runtime error in every hook point at once.
//
// Registration fails on a collision — two modules claiming one kind, or one
// registered twice. With the list fixed at compile time that is a wrong build
// rather than bad input, and the hook points respond to it by declining to
// enforce. So the failure mode of adding a colliding module is every guardrail
// in every project silently going quiet, which is the failure mode a guardrail
// engine can least afford. Here it is a red test instead.
func TestRegistry_RegistersCleanly(t *testing.T) {
	reg, err := Registry()
	require.NoError(t, err, "the shipped module list does not register")
	require.NotNil(t, reg)
	assert.NotEmpty(t, reg.DeclaredKinds(), "modules registered but declared no kinds — nothing can be bound to")
}

// TestAll_HoldsEveryModuleInTheRepo is the check that a module was written and
// the list was not updated.
//
// The other half of the problem is structural and no longer needs a test:
// module.NewRegistry takes a token only packages under internal/module/ can
// name, so the binary CANNOT be running a list other than this one. What that
// does not catch is a module that exists and was never added — it compiles,
// declares its kinds, and is simply absent, so the binary and every test agree
// perfectly on a vocabulary missing an entry. Agreement is not correctness when
// both sides read the same incomplete list.
//
// Ground truth therefore has to come from outside the list, and the ONLY honest
// statement of "this is a module" is that its type satisfies module.Module.
// That is what this asks, by type-checking the repo — not by matching a
// filename. An earlier version of this test scanned for internal/*mod
// directories and derived the module name by trimming that suffix, which was
// wrong three ways at once: it missed internal/mcp and anything nested, it
// accused a correctly-wired module whose Name() did not match its directory,
// and it accused a directory that merely ended in "mod" and held no module at
// all. It also reintroduced exactly what registry.go argues against — a naming
// convention every author "would both have to remember to follow" — and the
// only place that convention was ever written down was the test that stopped
// guarding the moment it was broken.
//
// Nothing here is imported. go/importer in source mode type-checks a package
// from disk without this package depending on it, which is what makes the
// enumeration possible at all: importing every module to look at it would be
// the second list again, written in import statements.
func TestAll_HoldsEveryModuleInTheRepo(t *testing.T) {
	listed := make(map[string]bool, len(All()))
	for _, m := range All() {
		listed[m.Name()] = true
	}

	for _, found := range modulesInRepo(t) {
		assert.Truef(t, listed[found.name],
			"%s declares %s, a module.Module named %q, and it is not in All() — the binary cannot produce its events, and because every test reads All() too, no test would notice",
			found.pkg, found.typ, found.name)
	}
}

// foundModule is one module.Module implementation discovered in the repo.
type foundModule struct {
	pkg  string // import path
	typ  string // the type implementing module.Module, as written
	name string // what its Name() returns
}

// modulesInRepo type-checks every package in this repo and returns the types
// that satisfy module.Module.
//
// Names are never derived from where a type lives. Nothing in module.Module
// constrains Name() to resemble its package, and assuming otherwise is what
// made an earlier version of this test accuse a correctly-wired module of being
// absent. A module already in All() is asked directly — Name() called on the
// real value, matched to what the type-checker found by import path and type
// name. One NOT in All() cannot be called without importing it, so its package
// Name constant is read instead, and if it has none the type is still reported
// under its import path: a missing module is worth failing over whether or not
// its name can be established without running it.
//
// Unexported types count. Scope().Names() returns them, and a module reached
// only through a constructor returning the interface is still a module in the
// build.
func modulesInRepo(t *testing.T) []foundModule {
	t.Helper()

	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "source", nil)

	base, err := imp.Import(modulePkgPath)
	require.NoError(t, err, "type-check %s — the discovery this test depends on is broken, and it would pass for that reason alone", modulePkgPath)

	obj := base.Scope().Lookup("Module")
	require.NotNil(t, obj, "%s declares no type Module", modulePkgPath)
	iface, ok := obj.Type().Underlying().(*types.Interface)
	require.True(t, ok, "%s.Module is not an interface", modulePkgPath)

	// Every module in All() answers for itself, so its type is known to be a
	// module without type-checking anything. Used below to name what the
	// type-checker finds.
	nameOfType := map[string]string{}
	for _, m := range All() {
		nameOfType[typeKey(m)] = m.Name()
	}

	var found []foundModule
	for _, path := range repoPackages(t) {
		pkg, err := imp.Import(path)
		if err != nil {
			// A package that does not type-check is a broken build, which the
			// build itself reports far better than this test would. Not a
			// module discovery failure.
			continue
		}
		for _, n := range pkg.Scope().Names() {
			tn, isType := pkg.Scope().Lookup(n).(*types.TypeName)
			if !isType {
				continue
			}
			T := tn.Type()
			if _, isIface := T.Underlying().(*types.Interface); isIface {
				// An interface embedding or restating module.Module satisfies
				// it and is not a module — module.Module itself is the obvious
				// one. Only a concrete type can be put in a list.
				continue
			}
			// Pointer receiver is the ordinary shape here (both shipped modules
			// use it), but the interface is what is being asked about, so both
			// forms count.
			written := n
			if !types.Implements(T, iface) {
				if !types.Implements(types.NewPointer(T), iface) {
					continue
				}
				written = "*" + n
			}
			found = append(found, foundModule{
				pkg:  path,
				typ:  written,
				name: moduleName(pkg, n, nameOfType[path+"."+n]),
			})
		}
	}

	require.NotEmpty(t, found, "type-checked the repo and found no module.Module implementations at all — discovery is broken, and this test would pass for that reason alone")
	return found
}

// moduleName works out what a discovered type's Name() returns.
//
// known wins: it came from calling Name() on the real thing. Failing that — the
// case that matters, a module NOT in All() — the package's exported Name
// constant is read, which is where both shipped modules put it and what their
// Name() returns. Failing even that the type is still reported, named by where
// it lives, because a module the list is missing is worth failing over whether
// or not its name can be determined without running it.
func moduleName(pkg *types.Package, typeName, known string) string {
	if known != "" {
		return known
	}
	if c, ok := pkg.Scope().Lookup("Name").(*types.Const); ok {
		return strings.Trim(c.Val().String(), `"`)
	}
	return pkg.Path() + "." + typeName
}

// typeKey identifies a module's concrete type as "<import path>.<type name>",
// which is how the type-checked scan above names what it finds. That is what
// lets a module already in All() be matched to its type on disk and answer for
// its own Name() rather than have one guessed for it.
func typeKey(m module.Module) string {
	T := reflect.TypeOf(m)
	for T.Kind() == reflect.Ptr {
		T = T.Elem()
	}
	return T.PkgPath() + "." + T.Name()
}

// repoPackages lists this repo's own packages, from the toolchain rather than
// by walking directories. `go list` knows what a package is — which
// directories hold Go code, which are nested, which are excluded by build
// tags — and a hand-rolled walk gets each of those subtly wrong. Nesting is the
// one that bit the version of this test that walked internal/ itself: it read a
// single directory level, so a module one level down was invisible.
//
// From the module root, not from this test's directory: `go list ./...` is
// relative to the working directory, and run here it would return this package
// alone — discovery finding nothing and the test passing for that reason. The
// require below is what turns that into a failure rather than a green run.
func repoPackages(t *testing.T) []string {
	t.Helper()

	root, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	require.NoError(t, err, "locate the module root — discovery depends on it")

	list := exec.Command("go", "list", "./...")
	list.Dir = strings.TrimSpace(string(root))
	out, err := list.Output()
	require.NoError(t, err, "go list ./... — discovery depends on it")

	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	require.NotEmpty(t, paths, "go list ./... returned nothing")
	return paths
}
