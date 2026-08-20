package declaration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This test reads the REAL examples/ tree, the opposite job to store_test.go's
// temp-root cases: it proves the shipped example composites — the file-guards,
// contexts and gates a consumer copies — reconcile with this loader and the module
// vocabulary the shipped build has. A `match` that names a field the kind does not
// carry, a trigger on a kind its nature does not admit, a `require` naming a
// context that does not exist: each would make the example Invalid here, so this
// is the guard that keeps a shipped example from silently not-loading.
//
// It is deliberately at the LOAD level rather than driving each example through a
// mock: the enter/exit/check SCRIPTS are exercised by the e2e suites
// (tests/e2e/fileguard, tests/e2e/context) with equivalent fixtures; what this
// adds is that the real declarations the examples ship parse and compile against
// the real scopes. The two together cover "the examples dispatch correctly" — the
// mechanism end to end in e2e, the shipped declarations here.

// examplesRoot finds the repo's examples/ directory from this test's location
// (internal/declaration/).
func examplesRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	root := filepath.Join(wd, "..", "..", "examples")
	if _, err := os.Stat(root); err != nil {
		t.Skipf("examples directory not found at %s: %v", root, err)
	}
	return root
}

// TestExamples_ShippedCompositesLoadClean loads each example's `.sloprail` and
// asserts nothing became Invalid — every shipped file-guard, context and gate
// parses and compiles against the real module vocabulary.
//
// The composites named in the dispatch-natures work are checked by name below so
// a regression in one is a named failure, but EVERY example with a `.sloprail` is
// loaded, so a new example that does not load is caught too.
func TestExamples_ShippedCompositesLoadClean(t *testing.T) {
	root := examplesRoot(t)
	reg := testRegistry(t)

	entries, err := os.ReadDir(root)
	require.NoError(t, err)

	loadedAny := false
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "deprecated" {
			continue
		}
		dotDir := filepath.Join(root, entry.Name(), ".sloprail")
		if _, err := os.Stat(dotDir); err != nil {
			continue // an example without new-format declarations
		}
		loadedAny = true

		t.Run(entry.Name(), func(t *testing.T) {
			loaded, err := New(dotDir).Load(reg)
			require.NoError(t, err, "the example's .sloprail must be readable")

			// Nothing may be Invalid — a shipped example that does not load is a
			// shipped example that guards nothing.
			for _, iv := range loaded.Invalid {
				t.Errorf("example %s: declaration %s did NOT load: %s",
					entry.Name(), iv.Qualified(), iv.Reason)
			}
		})
	}
	assert.True(t, loadedAny, "expected at least one example with a .sloprail to load")
}

// TestExamples_EvalLoopMaxing loads the eval-loop-maxing composite specifically
// and asserts its shape: the goal-tracking context (Post-triggered), the recording
// context, and the goal-verify gate that requires the context.
func TestExamples_EvalLoopMaxing(t *testing.T) {
	root := examplesRoot(t)
	reg := testRegistry(t)

	loaded, err := New(filepath.Join(root, "eval-loop-maxing", ".sloprail")).Load(reg)
	require.NoError(t, err)
	require.Empty(t, loaded.Invalid, "the eval-loop-maxing composite must load clean")

	// The goal-tracking context is present and enters on a Post event (a goal's
	// enabled flag exists only once the write settled).
	gt := findContext(loaded.Contexts, "goal-tracking")
	require.NotNil(t, gt, "goal-tracking context must load")
	assert.NotEmpty(t, gt.Enter)
	assert.NotEmpty(t, gt.Exit)

	// The goal-verify gate requires the goal-tracking context (the ordering that
	// makes the gate see the goal the context recognised).
	gv := findGate(loaded.Gates, "goal-verify")
	require.NotNil(t, gv, "goal-verify gate must load")
	assert.True(t, requiresContext(gv.Require, "goal-tracking"),
		"goal-verify must require the goal-tracking context")
}

// TestExamples_DeterministicRefactoring loads the deterministic-refactoring-mode
// composite: a refactoring context and a PREVENTIVE file-guard whose match reads
// context["refactoring"].active AND a marker quantifier — the guard→context link.
func TestExamples_DeterministicRefactoring(t *testing.T) {
	root := examplesRoot(t)
	reg := testRegistry(t)

	loaded, err := New(filepath.Join(root, "deterministic-refactoring-mode", ".sloprail")).Load(reg)
	require.NoError(t, err)
	require.Empty(t, loaded.Invalid, "the deterministic-refactoring composite must load clean")

	require.NotNil(t, findContext(loaded.Contexts, "refactoring"), "refactoring context must load")

	fg := findFileGuard(loaded.FileGuards, "moved-content-reconciles")
	require.NotNil(t, fg, "moved-content-reconciles file-guard must load")
	// It is preventive, and its match reads a context (the guard→context link) — the
	// compile against the file scope succeeding is what the clean load proves.
	assert.True(t, fg.Preventive, "the guard is preventive")
	assert.Contains(t, fg.Match, `context["refactoring"].active`,
		"the guard's match reads the refactoring context's active flag")
}

// TestExamples_InterlinkingAndResearch loads the interlinking and research-rigor
// composites — a context that enters on Post file events (interlinking) and one on
// PostTagWrite (research-rigor), each paired with a gate that requires it.
func TestExamples_InterlinkingAndResearch(t *testing.T) {
	root := examplesRoot(t)
	reg := testRegistry(t)

	for _, tc := range []struct {
		dir     string
		context string
		gate    string
	}{
		{"interlinking", "people-linked", "verify-linked"},
		{"research-rigor", "research-run", "depth-check"},
		{"completeness-artifact-on-trigger", "tag-declared", "verify-artifact-produced"},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			loaded, err := New(filepath.Join(root, tc.dir, ".sloprail")).Load(reg)
			require.NoError(t, err)
			require.Empty(t, loaded.Invalid, "the %s composite must load clean", tc.dir)

			require.NotNil(t, findContext(loaded.Contexts, tc.context),
				"%s context must load", tc.context)
			gate := findGate(loaded.Gates, tc.gate)
			require.NotNil(t, gate, "%s gate must load", tc.gate)
			assert.True(t, requiresContext(gate.Require, tc.context),
				"%s must require the %s context", tc.gate, tc.context)
		})
	}
}

// -- small finders over the loaded slices --

func findContext(cs []Context, name string) *Context {
	for i := range cs {
		if cs[i].Name == name {
			return &cs[i]
		}
	}
	return nil
}

func findGate(gs []Gate, name string) *Gate {
	for i := range gs {
		if gs[i].Name == name {
			return &gs[i]
		}
	}
	return nil
}

func findFileGuard(fgs []FileGuard, name string) *FileGuard {
	for i := range fgs {
		if fgs[i].Name == name {
			return &fgs[i]
		}
	}
	return nil
}

func requiresContext(reqs []Prerequisite, name string) bool {
	for _, r := range reqs {
		if r.Context == name {
			return true
		}
	}
	return false
}
