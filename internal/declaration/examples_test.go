package declaration

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/module/modules"
)

// This test loads EVERY real example under examples/**/.sloprail and asserts each
// loads clean under this loader. It is the proof that the example reconciliation
// worked: the shipped examples use the spec-faithful match grammar the current
// scopes admit, not the older vocabulary they were written against.
//
// It is the one test that reads real fixture directories on disk rather than
// declarations written inline — deliberately, because its whole job is to check
// the REAL files, the ones an author copies from and the ones the spec's examples
// are. If this passes, a person can point sloprail at any example's `.sloprail`
// and it loads; if the example grammar drifts back to the older vocabulary, this
// fails by name.
//
// Only NEW-format examples are loaded — those with a `.sloprail/{file-guard,gate,
// context,goal}` layout. The deprecated examples under examples/deprecated/ use
// the OLD `.sloprail/guardrails/<name>/GUARDRAIL.md` format, which this loader does
// not read (and correctly ignores), so they are not part of this proof.

// repoRoot walks up from the test's working directory to the module root — the
// directory holding go.mod — so the examples path is found wherever the test is
// run from.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no go.mod up the tree)")
		}
		dir = parent
	}
}

// newFormatExampleRoots finds every examples/*/.sloprail that uses the new
// declaration format — i.e. contains at least one of the four nature folders or
// the structure singleton. A `.sloprail` holding only the old `guardrails/` layout
// is skipped.
func newFormatExampleRoots(t *testing.T) []string {
	t.Helper()
	examplesDir := filepath.Join(repoRoot(t), "examples")
	entries, err := os.ReadDir(examplesDir)
	require.NoError(t, err)

	var roots []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "deprecated" {
			continue
		}
		dotdir := filepath.Join(examplesDir, e.Name(), ".sloprail")
		if !isNewFormat(dotdir) {
			continue
		}
		roots = append(roots, dotdir)
	}
	sort.Strings(roots)
	return roots
}

// isNewFormat reports whether a `.sloprail` directory holds any new-format
// declaration — a nature folder or the structure singleton.
func isNewFormat(dotdir string) bool {
	for _, sub := range []string{dirFileGuard, dirGate, dirContext, dirGoal} {
		if info, err := os.Stat(filepath.Join(dotdir, sub)); err == nil && info.IsDir() {
			return true
		}
	}
	if _, err := os.Stat(filepath.Join(dotdir, dirFileGuard, fileStructure)); err == nil {
		return true
	}
	return false
}

// Every shipped new-format example loads clean under the real module registry —
// no invalid declarations. This is the reconciliation proof.
func TestExamples_AllLoadClean(t *testing.T) {
	reg, err := modules.Registry()
	require.NoError(t, err)

	roots := newFormatExampleRoots(t)
	require.NotEmpty(t, roots, "expected to find new-format examples to load")

	for _, root := range roots {
		root := root
		name := filepath.Base(filepath.Dir(root)) // the example's directory name
		t.Run(name, func(t *testing.T) {
			loaded, err := New(root).Load(reg)
			require.NoError(t, err)
			assert.Empty(t, loaded.Invalid,
				"example %q must load clean; invalid declarations:\n%s",
				name, joinReasons(loaded.Invalid))
		})
	}
}

// The examples collectively exercise every nature and both aliases — a sanity
// check that the reconciliation proof is loading a representative set, not an
// accidentally-empty one. Counts across ALL examples.
func TestExamples_CoverEveryNature(t *testing.T) {
	reg, err := modules.Registry()
	require.NoError(t, err)

	var (
		fileGuards, gates, contexts, goals, structures int
	)
	for _, root := range newFormatExampleRoots(t) {
		loaded, err := New(root).Load(reg)
		require.NoError(t, err)
		require.Empty(t, loaded.Invalid, "example %s: %s", root, joinReasons(loaded.Invalid))
		fileGuards += len(loaded.FileGuards)
		gates += len(loaded.Gates)
		contexts += len(loaded.Contexts)
		goals += len(loaded.Goals)
		if loaded.Structure != nil {
			structures++
		}
	}

	assert.Positive(t, fileGuards, "the examples include file-guards")
	assert.Positive(t, gates, "the examples include gates")
	assert.Positive(t, contexts, "the examples include contexts")
	assert.Positive(t, goals, "the examples include goals")
	assert.Positive(t, structures, "the examples include a structure gate")
}

// The eval-loop-maxing example is the composite the spec calls out — a goal, its
// paired tracking context, a recording context, and a verify gate. Loading it
// clean proves the cross-nature `require: [{context}]` resolution works on a real
// example, since the goal-verify gate requires the goal-tracking context.
func TestExamples_EvalLoopMaxingResolvesCrossNatureRequire(t *testing.T) {
	reg, err := modules.Registry()
	require.NoError(t, err)

	root := filepath.Join(repoRoot(t), "examples", "eval-loop-maxing", ".sloprail")
	loaded, err := New(root).Load(reg)
	require.NoError(t, err)
	require.Empty(t, loaded.Invalid, "%s", joinReasons(loaded.Invalid))

	// The gate requires a context by name — that name resolved, or the gate would
	// be Invalid with ErrUnknownContext.
	require.Len(t, loaded.Gates, 1)
	require.Len(t, loaded.Gates[0].Require, 1)
	assert.Equal(t, "goal-tracking", loaded.Gates[0].Require[0].Context)

	// And the goal it is built around loaded.
	require.Len(t, loaded.Goals, 1)
	assert.Equal(t, "accuracy-target", loaded.Goals[0].Name)
}

func joinReasons(invalid []Invalid) string {
	var b []byte
	for _, iv := range invalid {
		b = append(b, "  "...)
		b = append(b, iv.Qualified()...)
		b = append(b, ": "...)
		b = append(b, iv.Reason...)
		b = append(b, '\n')
	}
	return string(b)
}
