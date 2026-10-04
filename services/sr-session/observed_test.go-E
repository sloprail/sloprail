package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/module"
)

// treeDifference is the first implementation of filemod.Observed, and these are
// the contract's terms rather than this type's internals — a second producer
// should be able to be checked against the same claims.

// TestTreeDifference_SatisfiesObserved: a compile-time statement that this is
// the interface's implementation, so a change to either side is a build error
// rather than a runtime surprise.
func TestTreeDifference_SatisfiesObserved(t *testing.T) {
	var _ filemod.Observed = (*treeDifference)(nil)
}

// TestTreeDifference_PathsAreCleanAndBaselineIsKeyedOnThem.
//
// The contract's sharpest term: ExistedAtBaseline is asked with the CANONICAL
// spelling — filepath.Clean of what Paths gave — and a producer keying its map
// on raw spellings answers false for a file that was there. Every update then
// ships as a create and every delete disappears, with nothing in the tree
// contradicting any of it.
//
// Checked the way the module checks it: every path Paths reports is its own
// clean form, and the baseline answers for exactly that spelling.
func TestTreeDifference_PathsAreCleanAndBaselineIsKeyedOnThem(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(proj, "deep", "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "deep", "nested", "was-here.md"), []byte("one"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")
	base := runGit(t, proj, "rev-parse", "HEAD")

	require.NoError(t, os.WriteFile(filepath.Join(proj, "deep", "nested", "was-here.md"), []byte("two"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "deep", "nested", "is-new.md"), []byte("new"), 0o644))

	d, err := newTreeDifference(proj, base)
	require.NoError(t, err)
	require.NotEmpty(t, d.Paths())

	for _, p := range d.Paths() {
		assert.Equal(t, filepath.Clean(p), p, "every reported path must already be canonical")
		assert.False(t, filepath.IsAbs(p), "paths are repository-relative")

		// The map answers for the spelling that was reported. A producer keyed
		// on anything else would fall through to the zero value here, which is
		// the silent always-false failure the contract names.
		_, held := d.baseline[p]
		assert.Truef(t, held, "the baseline is not keyed on the spelling %q that Paths reported", p)
	}

	assert.True(t, d.ExistedAtBaseline("deep/nested/was-here.md"))
	assert.False(t, d.ExistedAtBaseline("deep/nested/is-new.md"))
}

// TestTreeDifference_CleansNonCanonicalSpellings.
//
// The cleaning guarantee, fed the input that can break it.
//
// Git's own paths are already canonical, so every test above exercises the
// cleaning as a no-op and would pass against a producer that keyed its map on
// the raw spelling — the exact defect ErrBaselineKeyedOnRawSpelling exists to
// catch, and one this file could not otherwise see. These spellings are
// therefore supplied directly: "./a.md" and "dir/./a.md" are what a producer
// that built its paths by joining rather than by asking git would hold.
//
// Two claims, and the second is the one with teeth: the reported path is clean,
// AND the baseline answers for that clean spelling. A producer failing the
// second returns false for a file that was at the baseline, so every update
// ships as a create and every delete vanishes — with the tree agreeing with
// each of those events.
func TestTreeDifference_CleansNonCanonicalSpellings(t *testing.T) {
	d := differenceOf("/repo", []gitrepo.Change{
		{Path: "./a.md", ExistedAtBaseline: true},
		{Path: "dir/./b.md", ExistedAtBaseline: true},
		{Path: "dir//c.md", ExistedAtBaseline: false},
		{Path: "./dir/sub/../d.md", ExistedAtBaseline: true},
	})

	assert.Equal(t, []string{"a.md", "dir/b.md", "dir/c.md", "dir/d.md"}, d.Paths(),
		"every reported path must be the clean spelling")

	for _, p := range d.Paths() {
		require.Equal(t, filepath.Clean(p), p)
		_, held := d.baseline[p]
		assert.Truef(t, held, "the baseline must answer for %q, the spelling Paths reported", p)
	}

	// The fact the tree cannot recover, read back through the canonical
	// spelling the module will actually ask with.
	assert.True(t, d.ExistedAtBaseline("a.md"), "a raw-keyed map answers false here, and every delete vanishes")
	assert.True(t, d.ExistedAtBaseline("dir/b.md"))
	assert.False(t, d.ExistedAtBaseline("dir/c.md"))
	assert.True(t, d.ExistedAtBaseline("dir/d.md"))

	// And the raw spellings are NOT keys. Asked with one, the module would be
	// asking a question this producer never claimed to answer.
	for _, raw := range []string{"./a.md", "dir/./b.md", "dir//c.md"} {
		_, held := d.baseline[raw]
		assert.Falsef(t, held, "the raw spelling %q must not be a key", raw)
	}
}

// TestTreeDifference_TwoSpellingsOfOneFileAreOneEntry.
//
// Spelling is not part of the contract: "a.md" and "./a.md" are one file and
// must yield one event. A producer emitting both would hand every rule the same
// file twice, and — worse — the two entries could carry different baselines,
// making the classification depend on which was listed first.
func TestTreeDifference_TwoSpellingsOfOneFileAreOneEntry(t *testing.T) {
	d := differenceOf("/repo", []gitrepo.Change{
		{Path: "a.md", ExistedAtBaseline: true},
		{Path: "./a.md", ExistedAtBaseline: false},
	})

	assert.Equal(t, []string{"a.md"}, d.Paths(), "one file, one entry")
	assert.True(t, d.ExistedAtBaseline("a.md"),
		"the first answer stands rather than the last, so the result does not depend on ordering")
}

// TestTreeDifference_DoesNotTripTheRawSpellingGuard.
//
// filemod reports ErrBaselineKeyedOnRawSpelling when a producer answers
// differently for a path's raw and canonical spellings. It is only diagnostic
// in one direction — the raw spelling finding something the canonical one did
// not — which no map keyed as the contract asks can produce.
//
// Run through the module itself rather than asserted about this type, because
// the guard lives there: this is the check a real producer meets.
func TestTreeDifference_DoesNotTripTheRawSpellingGuard(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(proj, "dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "dir", "a.md"), []byte("one"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "gone.md"), []byte("one"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")
	base := runGit(t, proj, "rev-parse", "HEAD")

	require.NoError(t, os.WriteFile(filepath.Join(proj, "dir", "a.md"), []byte("two"), 0o644))
	require.NoError(t, os.Remove(filepath.Join(proj, "gone.md")))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "dir", "b.md"), []byte("new"), 0o644))

	d, err := newTreeDifference(proj, base)
	require.NoError(t, err)

	events, err := filemod.New().Extract(module.Input{
		module.InputPhase:   module.PhasePost,
		module.InputPayload: d,
	})
	assert.NoError(t, err, "the module must find no fault with this producer")
	assert.Len(t, events, 3, "an update, a delete and a create")
}

// TestTreeDifference_ClassifiesThroughTheModule.
//
// The join this half exists for: the two facts this type supplies — the paths,
// and whether each was at the baseline — become the three Post kinds. Asserted
// on the events rather than on the struct, since the events are what a rule
// sees.
func TestTreeDifference_ClassifiesThroughTheModule(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "updated.md"), []byte("one"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "deleted.md"), []byte("one"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")
	base := runGit(t, proj, "rev-parse", "HEAD")

	require.NoError(t, os.WriteFile(filepath.Join(proj, "updated.md"), []byte("two"), 0o644))
	require.NoError(t, os.Remove(filepath.Join(proj, "deleted.md")))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "created.md"), []byte("new"), 0o644))

	d, err := newTreeDifference(proj, base)
	require.NoError(t, err)
	events, err := filemod.New().Extract(module.Input{
		module.InputPhase:   module.PhasePost,
		module.InputPayload: d,
	})
	require.NoError(t, err)

	byPath := map[string]string{}
	for _, e := range events {
		f, err := filemod.FromEvent(e)
		require.NoError(t, err)
		byPath[f.Path] = e.Kind
	}
	assert.Equal(t, filemod.KindPostUpdate, byPath["updated.md"])
	assert.Equal(t, filemod.KindPostDelete, byPath["deleted.md"])
	assert.Equal(t, filemod.KindPostCreate, byPath["created.md"])
}

// TestTreeDifference_RootIsTheRepositoryAndNeverEmpty.
//
// The module refuses an empty root rather than defaulting it, because joining
// against "" resolves the paths against the process's working directory — which
// would make the classification depend on where the hook happened to be
// invoked from.
func TestTreeDifference_RootIsTheRepositoryAndNeverEmpty(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "a.md"), []byte("x"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")
	base := runGit(t, proj, "rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(proj, "b.md"), []byte("y"), 0o644))

	d, err := newTreeDifference(proj, base)
	require.NoError(t, err)
	assert.NotEmpty(t, d.Root())

	// Compared after resolving symlinks rather than against the literal string.
	// The root comes from git, which reports a path with its symlinks resolved,
	// and on macOS the temp dir is reached through /var -> /private/var. What
	// the contract requires is that the root IS the repository, not that it is
	// spelled the way the caller happened to spell it.
	wantRoot, err := filepath.EvalSymlinks(proj)
	require.NoError(t, err)
	gotRoot, err := filepath.EvalSymlinks(d.Root())
	require.NoError(t, err)
	assert.Equal(t, wantRoot, gotRoot)

	// The paths the root is there to resolve actually resolve against it.
	for _, p := range d.Paths() {
		_, err := os.Stat(filepath.Join(d.Root(), p))
		assert.NoErrorf(t, err, "%q must resolve against the root", p)
	}
}

// TestTreeDifference_FromASubdirectoryStillRootsAtTheRepository is F1 at the
// consumer's boundary.
//
// The engine is invoked with the cwd the hook reported, which is wherever the
// agent was working — not necessarily the top of the tree. Every path the
// producer reports is repository-relative, so a difference rooted at the cwd
// resolves none of them and every modified file outside that subdirectory
// stats as absent. filemod.classify maps existed-before/absent-now to a
// DELETE, so the cycle dispatches a deletion for a file that is still on disk.
func TestTreeDifference_FromASubdirectoryStillRootsAtTheRepository(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(proj, "sub", "deep"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "top.md"), []byte("one"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "sub", "deep", "inner.md"), []byte("one"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")
	base := runGit(t, proj, "rev-parse", "HEAD")

	require.NoError(t, os.WriteFile(filepath.Join(proj, "top.md"), []byte("two"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "sub", "deep", "inner.md"), []byte("two"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "sub", "deep", "new.md"), []byte("fresh"), 0o644))

	// Asked from two levels down, the way a hook invoked there asks it.
	d, err := newTreeDifference(filepath.Join(proj, "sub", "deep"), base)
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"top.md", "sub/deep/inner.md", "sub/deep/new.md"}, d.Paths())

	// The property that actually protects the classification: every reported
	// path resolves against the reported root. Without it, top.md and
	// sub/deep/inner.md — both modified, both still present — are dispatched as
	// deletions.
	for _, p := range d.Paths() {
		_, err := os.Stat(filepath.Join(d.Root(), p))
		assert.NoErrorf(t, err, "%q does not resolve against the root, so it dispatches as a delete", p)
	}

	assert.True(t, d.ExistedAtBaseline("top.md"), "a modified file was at the baseline")
	assert.True(t, d.ExistedAtBaseline("sub/deep/inner.md"), "a modified file was at the baseline")
	assert.False(t, d.ExistedAtBaseline("sub/deep/new.md"), "a new file was not at the baseline")
}

// TestTreeDifference_UnchangedTreeIsEmpty: untouched_stays_silent at the
// producer's own boundary. Everything this reports becomes an event with no
// second filter downstream, so a path that has not changed must not be here at
// all.
func TestTreeDifference_UnchangedTreeIsEmpty(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "a.md"), []byte("x"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")
	base := runGit(t, proj, "rev-parse", "HEAD")

	d, err := newTreeDifference(proj, base)
	require.NoError(t, err)
	assert.Empty(t, d.Paths())
	assert.True(t, d.empty())
}

// TestTreeDifference_ReportsEachFileOnce.
//
// Everything Paths names becomes an event, and the module deduplicates on the
// canonical spelling — but a producer naming one file twice would still have
// been a producer that disagreed with itself. One file, one entry, from here.
func TestTreeDifference_ReportsEachFileOnce(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "twice.md"), []byte("one"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")
	base := runGit(t, proj, "rev-parse", "HEAD")

	// Untracked in the index but still in the commit, and rewritten on disk:
	// the one state both of git's answers name.
	runGit(t, proj, "rm", "--cached", "twice.md")
	require.NoError(t, os.WriteFile(filepath.Join(proj, "twice.md"), []byte("rewritten"), 0o644))

	d, err := newTreeDifference(proj, base)
	require.NoError(t, err)

	seen := map[string]int{}
	for _, p := range d.Paths() {
		seen[p]++
	}
	assert.Equal(t, 1, seen["twice.md"], "one file must be one entry")
}
