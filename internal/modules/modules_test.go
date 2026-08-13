package modules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// TestAll_HoldsEveryModulePackage is the check that a module was added and the
// list was not.
//
// Everything else about the list is now structural: one function, and both the
// binary and the tests call it. What sharing cannot catch is a module that
// exists and was never added — filemod compiles, declares its kinds, and is
// simply absent, so the binary and every test agree perfectly on a vocabulary
// missing an entry. Agreement is not correctness when both sides read the same
// incomplete list.
//
// Ground truth has to come from outside the list, so it comes from the tree:
// internal/<name>mod is how a module package is named here, and each one is
// expected in All. Discovery by convention rather than by reflection over
// interfaces — a Go test cannot enumerate the types implementing an interface
// in packages it does not import, and importing them would be the second list
// again.
func TestAll_HoldsEveryModulePackage(t *testing.T) {
	listed := make(map[string]bool, len(All()))
	for _, m := range All() {
		listed[m.Name()] = true
	}

	for _, pkg := range modulePackages(t) {
		// internal/filemod holds the module named "file" — the package suffix
		// is the convention, the Name is the module's own.
		name := strings.TrimSuffix(pkg, "mod")
		assert.Truef(t, listed[name],
			"internal/%s exists but module %q is not in All() — the binary cannot produce its events and no test would notice, because every test reads All() too",
			pkg, name)
	}
}

// modulePackages returns the internal/*mod directories: the modules this repo
// has, as opposed to the ones the list remembers.
func modulePackages(t *testing.T) []string {
	t.Helper()

	// From internal/modules to internal.
	entries, err := os.ReadDir(filepath.Join("..", ""))
	require.NoError(t, err)

	var pkgs []string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), "mod") {
			continue
		}
		pkgs = append(pkgs, e.Name())
	}
	require.NotEmpty(t, pkgs, "found no internal/*mod packages — the discovery this test depends on is broken, and it would pass for that reason alone")
	return pkgs
}
