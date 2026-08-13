package modules

import (
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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

	_, imp := sourceImporter(t)

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

// TestNewRegistry_RejectsAForgedToken pins the one property the fence rests on
// and the compiler is the only witness to.
//
// The fence is two rules that have to hold at once. Go's `internal` rule stops
// a package outside internal/module/ from NAMING registryauth.Token — that half
// was always true. The other half is that nobody can produce a value of that
// type without naming it, and for a while that half was false: Token was a bare
// `struct{}`, assignability is structural for unnamed types, and so
//
//	module.NewRegistry(struct{}{}, modules.All()[:1]...)
//
// built clean and vetted clean from services/. A hook point could enforce
// against a module list of its own with no new type anywhere — nothing for
// TestAll_HoldsEveryModuleInTheRepo above to find, nothing `guardrail help`
// prints, nothing any test reads. The unexported field in Token is what closed
// it. This test is what keeps it closed.
//
// It has to be a type-check rather than an ordinary assertion because the
// property IS "does not compile", and a test that compiles cannot contain the
// code it is about. So the snippet is type-checked as data and the assertion is
// that the checker refuses it. A test that merely called
// NewRegistry(registryauth.Grant(), ...) would prove nothing: this package is
// inside the fence, where everything is allowed.
//
// This covers the assignability half only, and that is on purpose rather than
// an omission. The `internal` rule is enforced by the go command, not by
// go/types — a snippet importing registryauth from a services/ path type-checks
// here perfectly happily, while `go build` on the same file reports "use of
// internal package ... not allowed". An earlier draft of this test asserted the
// naming half too and failed for exactly that reason. So the two halves have
// two different witnesses: the go command already refuses the naming half on
// every build, and TestFence_InternalRuleBlocksNamingTheToken below pins it by
// asking the go command rather than the type-checker. Here, assignability.
//
// Failing means an outside caller can forge a token, and every argument written
// in registryauth, modules.go and module.go about there being exactly one
// module list in a build is false.
func TestNewRegistry_RejectsAForgedToken(t *testing.T) {
	const outside = "github.com/sloprail/sloprail/services/notreal"

	for _, tc := range []struct {
		name    string
		call    string
		wantErr string
	}{
		{
			// The bypass itself: a composite literal of the same shape,
			// assignable to Token if and only if Token has no unexported field.
			name:    "forged token, structurally identical",
			call:    "module.NewRegistry(struct{}{}, modules.All()...)",
			wantErr: "cannot use struct{}{}",
		},
		{
			// The sharper form, and the one no other test can catch: a
			// divergent list built from modules that all already exist, so
			// discovery has no unlisted type to find.
			name:    "forged token, divergent list of existing modules",
			call:    "module.NewRegistry(struct{}{}, modules.All()[:1]...)",
			wantErr: "cannot use struct{}{}",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No registryauth import: the whole point of the bypass is that it
			// never names the token type. A snippet that imported it would be
			// testing the other half, and would not compile from here anyway.
			src := "package notreal\n\n" +
				"import (\n" +
				"\t\"github.com/sloprail/sloprail/internal/module\"\n" +
				"\t\"github.com/sloprail/sloprail/internal/module/modules\"\n" +
				")\n\n" +
				"var _ = func() { _, _ = " + tc.call + " }\n"

			err := typeCheckSnippet(t, outside, src)
			require.Error(t, err, "this snippet type-checks, so a hook point can build a module list of its own:\n%s", src)
			assert.Contains(t, err.Error(), tc.wantErr,
				"refused, but not for the reason the fence claims — a snippet rejected over a typo would pass this test while the fence was wide open")
		})
	}

	// The control. Same harness, same imports, a call that SHOULD compile from
	// inside the fence — without it every case above would pass just as well if
	// typeCheckSnippet were broken and rejected everything handed to it.
	t.Run("the harness admits a legitimate call", func(t *testing.T) {
		src := "package modulesctl\n\n" +
			"import (\n" +
			"\t\"github.com/sloprail/sloprail/internal/module\"\n" +
			"\t\"github.com/sloprail/sloprail/internal/module/modules\"\n" +
			"\t\"github.com/sloprail/sloprail/internal/module/internal/registryauth\"\n" +
			")\n\n" +
			"var _ = func() { _, _ = module.NewRegistry(registryauth.Grant(), modules.All()...) }\n"

		err := typeCheckSnippet(t, "github.com/sloprail/sloprail/internal/module/modulesctl", src)
		assert.NoError(t, err, "the legitimate call does not type-check either, so the refusals above prove nothing — the harness rejects whatever it is handed")
	})
}

// TestFence_InternalRuleBlocksNamingTheToken pins the other half of the fence,
// with the only tool that can see it.
//
// go/types does not implement the `internal` rule — it is the go command's,
// applied when resolving an import path, so the test above cannot express this
// and reported a false green when it tried. The go command can, so this asks
// the go command: write a package under services/ that names the token, build
// it, and require the refusal.
//
// Why pin something the language guarantees: the guarantee is conditional on
// registryauth's PATH, and a path is an ordinary thing to change. Moved out
// from under internal/module/internal/ — during a refactor, to break an import
// cycle, because a second package wanted a token — it keeps compiling, keeps
// passing every other test, and silently becomes importable from anywhere. The
// unexported field would still stop `struct{}{}`, but any package could then
// call Grant. This fails the moment that move happens.
func TestFence_InternalRuleBlocksNamingTheToken(t *testing.T) {
	root := repoRoot(t)

	// Under services/, which is where the hook points live and where the hole
	// this fence closed was found. Removed whatever the outcome.
	dir := filepath.Join(root, "services", "fenceprobe")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	src := "package fenceprobe\n\n" +
		"import (\n" +
		"\t\"github.com/sloprail/sloprail/internal/module\"\n" +
		"\t\"github.com/sloprail/sloprail/internal/module/modules\"\n" +
		"\t\"github.com/sloprail/sloprail/internal/module/internal/registryauth\"\n" +
		")\n\n" +
		"var _ = func() { _, _ = module.NewRegistry(registryauth.Grant(), modules.All()...) }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "probe.go"), []byte(src), 0o644))

	build := exec.Command("go", "build", "./services/fenceprobe/")
	build.Dir = root
	out, err := build.CombinedOutput()

	require.Errorf(t, err, "a package under services/ imported registryauth and built:\n%s\n\nthe fence depends on registryauth sitting under internal/module/internal/, and it no longer does", src)
	assert.Contains(t, string(out), "use of internal package",
		"the build failed for some other reason, so this test is not watching the fence:\n%s", out)
}

// repoRoot is the module root, asked of the toolchain rather than derived from
// this test's own path, which is what repoPackages does for the same reason.
func repoRoot(t *testing.T) string {
	t.Helper()

	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	require.NoError(t, err, "locate the module root")
	return strings.TrimSpace(string(out))
}

// typeCheckSnippet type-checks src as if it were the package at importPath,
// resolving its imports from this repo's real source.
//
// Nothing is written to disk and nothing is added to the build — the snippet
// exists only as a string, which is what lets a test assert on code that by
// definition cannot compile. All errors are collected rather than stopping at
// the first, so the caller can match on the one it means.
func typeCheckSnippet(t *testing.T, importPath, src string) error {
	t.Helper()

	fset, imp := sourceImporter(t)
	f, err := parser.ParseFile(fset, "snippet.go", src, 0)
	require.NoError(t, err, "the snippet does not parse — fix the test, it is not saying anything about the fence")

	var errs []string
	conf := types.Config{
		Importer: imp,
		Error:    func(err error) { errs = append(errs, err.Error()) },
	}
	_, _ = conf.Check(importPath, fset, []*ast.File{f}, nil)

	if len(errs) == 0 {
		return nil
	}
	return errors.New(strings.Join(errs, "\n"))
}

// sourceImporter returns the one source importer this package's tests share.
//
// Type-checking from source is the expensive thing here — it compiles the
// dependency graph of whatever it is asked for, and both the repo scan and each
// forged-token snippet ask for internal/module and its transitive imports. A
// fresh importer per call redoes all of it; go/importer caches per instance, so
// one instance means the graph is checked once and every later Import is a map
// hit. That is the whole reason this is shared rather than the tests being
// gated behind a build tag or -short: the suite this guards catches a module
// silently absent from the build, which is exactly the failure that survives
// when a test only runs in CI.
//
// Safe to share because these tests do not run in parallel — no t.Parallel in
// this file — and go/types only ever reads from it. Add t.Parallel and this
// needs a mutex or a per-test importer.
//
// The fset is returned with it: an importer is bound to the one it was built
// with, and parsing a snippet into a different fset yields positions the
// checker cannot resolve.
func sourceImporter(t *testing.T) (*token.FileSet, types.Importer) {
	t.Helper()

	sharedImporterOnce.Do(func() {
		sharedFset = token.NewFileSet()
		sharedImporter = importer.ForCompiler(sharedFset, "source", nil)
	})
	return sharedFset, sharedImporter
}

var (
	sharedImporterOnce sync.Once
	sharedFset         *token.FileSet
	sharedImporter     types.Importer
)

// repoPackages lists this repo's own packages, from the toolchain rather than
// by walking directories. `go list` knows what a package is — which
// directories hold Go code, which are nested, which files the build context
// selects — and a hand-rolled walk gets each of those subtly wrong. Nesting is
// the one that bit the version of this test that walked internal/ itself: it
// read a single directory level, so a module one level down was invisible.
//
// Build-tag handling is agreement with the build, not a guarantee on top of it.
// An earlier version of this comment claimed knowing "which are excluded by
// build tags" as a strength of using the toolchain. It is not one: `go list`
// resolves tags for ONE context, and this call gets the default. A module
// declared in a file behind `//go:build linux` is invisible to discovery — but
// it is equally absent from the binary this test's context builds, so the two
// still agree, which is all this test claims. The gap is real the moment a
// second context ships: build for linux and that module IS in the binary, with
// nothing checking it was added to All(). This repo has no build-tagged files
// at all and builds one context, so covering more would be machinery guarding
// nothing and guessing at a tag set nobody has written down. If a tagged file
// or a build matrix ever lands, this loop has to run per context.
//
// From the module root, not from this test's directory: `go list ./...` is
// relative to the working directory, and run here it would return this package
// alone — discovery finding nothing and the test passing for that reason. The
// require below is what turns that into a failure rather than a green run.
//
// It covers the DEFAULT build context only, and nothing more. `go list ./...`
// resolves build tags for the toolchain's own GOOS/GOARCH with no extra tags
// set, so a module in a file guarded by `//go:build linux` or
// `//go:build integration` is not in this list and discovery cannot see it. See
// TestAll_HoldsEveryModuleInTheRepo's note on what that does and does not
// guarantee.
func repoPackages(t *testing.T) []string {
	t.Helper()

	list := exec.Command("go", "list", "./...")
	list.Dir = repoRoot(t)
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
