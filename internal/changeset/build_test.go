package changeset

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func paths(fs []File) []string {
	out := []string{}
	for _, f := range fs {
		out = append(out, f.Path)
	}
	return out
}

func otherPaths(os []Other) map[string]string {
	out := map[string]string{}
	for _, o := range os {
		out[o.Path] = o.Status
	}
	return out
}

func fileNamed(t *testing.T, cs Changeset, path string) File {
	t.Helper()
	for _, f := range cs.Files {
		if f.Path == path {
			return f
		}
	}
	require.Failf(t, "file not in changeset", "%s not in %v", path, paths(cs.Files))
	return File{}
}

func TestBuild_SquashesTheRangeIntoOneNetDiffWithEveryCommitListed(t *testing.T) {
	dir := initRepo(t)
	base := put(t, dir, "seed", map[string]string{"a.go": "one\n"})
	put(t, dir, "first edit", map[string]string{"a.go": "one\ntwo\n"})
	head := put(t, dir, "second edit", map[string]string{"a.go": "one\ntwo\nthree\n"})

	cs, err := Build(dir, rng(base, head), Options{Scan: scan, Select: selectAll})
	require.NoError(t, err)
	assert.Equal(t, base, cs.Base)
	assert.Equal(t, head, cs.Head)
	require.Len(t, cs.Commits, 2)
	assert.Equal(t, "first edit", cs.Commits[0].Subject, "oldest first")
	assert.Equal(t, "second edit", cs.Commits[1].Subject)

	require.Len(t, cs.Files, 1)
	f := cs.Files[0]
	assert.Equal(t, "M", f.Status)
	assert.Equal(t, "one\n", f.OldContent)
	assert.Equal(t, "one\ntwo\nthree\n", f.NewContent)
	assert.Contains(t, f.Diff, "+two")
	assert.Contains(t, f.Diff, "+three", "one diff for the whole range, not per commit")
}

func TestBuild_ReadsCommitsNeverTheWorkingTree(t *testing.T) {
	dir := initRepo(t)
	base := put(t, dir, "seed", map[string]string{"a.go": "one\n"})
	head := put(t, dir, "edit", map[string]string{"a.go": "two\n"})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("dirty\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "scratch.go"), []byte("x"), 0o644))

	cs, err := Build(dir, rng(base, head), Options{Scan: scan, Select: selectAll})
	require.NoError(t, err)
	assert.Equal(t, []string{"a.go"}, paths(cs.Files))
	assert.Equal(t, "two\n", cs.Files[0].NewContent)
	assert.NotContains(t, cs.Files[0].Diff, "dirty")
}

func TestBuild_StatusesAndRenameCarriesOldPath(t *testing.T) {
	dir := initRepo(t)
	base := put(t, dir, "seed", map[string]string{
		"del.txt": "gone\n",
		"mv.txt":  "a long enough body that similarity keeps it\nline two\nline three\n",
	})
	git(t, dir, "mv", "mv.txt", "moved.txt")
	head := put(t, dir, "change", map[string]string{"add.txt": "new\n"})
	git(t, dir, "rm", "-q", "del.txt")
	git(t, dir, "commit", "-qm", "rm")
	head = git(t, dir, "rev-parse", "HEAD")

	cs, err := Build(dir, rng(base, head), Options{Deletions: IncludeDeletions, Scan: scan, Select: selectAll})
	require.NoError(t, err)

	mv := fileNamed(t, cs, "moved.txt")
	assert.Equal(t, "R", mv.Status)
	assert.Equal(t, "mv.txt", mv.OldPath)
	assert.Contains(t, mv.OldContent, "line two", "the old side is read from the old path")
	assert.Contains(t, mv.NewContent, "line two")

	add := fileNamed(t, cs, "add.txt")
	assert.Equal(t, "A", add.Status)
	assert.Equal(t, "", add.OldContent)

	del := fileNamed(t, cs, "del.txt")
	assert.Equal(t, "D", del.Status)
	assert.Equal(t, "gone\n", del.OldContent)
	assert.Equal(t, "", del.NewContent)
}

func deletionFixture(t *testing.T) (dir, base, head string) {
	dir = initRepo(t)
	base = put(t, dir, "seed", map[string]string{"keep.go": "k\n", "del.go": "d\n", "mv.go": "a long enough body that similarity keeps it\nline two\nline three\n"})
	git(t, dir, "mv", "mv.go", "moved.go")
	put(t, dir, "edit", map[string]string{"keep.go": "k2\n"})
	git(t, dir, "rm", "-q", "del.go")
	git(t, dir, "commit", "-qm", "rm")
	return dir, base, git(t, dir, "rev-parse", "HEAD")
}

func TestBuild_DeletionsSkipLeavesDeletedFilesInOthers(t *testing.T) {
	dir, base, head := deletionFixture(t)
	for _, mode := range []DeletionMode{"", SkipDeletions} {
		cs, err := Build(dir, rng(base, head), Options{Deletions: mode, Scan: scan, Select: selectAll})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"keep.go", "moved.go"}, paths(cs.Files), "the default is skip; a rename is not a deletion")
		assert.Equal(t, map[string]string{"del.go": "D"}, otherPaths(cs.Others))
	}
}

func TestBuild_DeletionsIncludeAdmitsDeletedFiles(t *testing.T) {
	dir, base, head := deletionFixture(t)
	cs, err := Build(dir, rng(base, head), Options{Deletions: IncludeDeletions, Scan: scan, Select: selectAll})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"keep.go", "moved.go", "del.go"}, paths(cs.Files))
	assert.Empty(t, cs.Others)
}

func TestBuild_DeletionsOnlyKeepsJustDeletedFiles(t *testing.T) {
	dir, base, head := deletionFixture(t)
	cs, err := Build(dir, rng(base, head), Options{Deletions: OnlyDeletions, Scan: scan, Select: selectAll})
	require.NoError(t, err)
	assert.Equal(t, []string{"del.go"}, paths(cs.Files))
	assert.Equal(t, map[string]string{"keep.go": "M", "moved.go": "R"}, otherPaths(cs.Others))
}

func TestBuild_MatchSelectsFilesAndTheRestAreOthersByName(t *testing.T) {
	dir := initRepo(t)
	base := put(t, dir, "seed", map[string]string{"a.go": "1", "README.md": "1"})
	head := put(t, dir, "edit", map[string]string{"a.go": "2", "README.md": "2", "new.go": "n"})

	cs, err := Build(dir, rng(base, head), Options{Scan: scan, Select: func(s Scope) (bool, error) {
		return filepath.Ext(s.Path) == ".go", nil
	}})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a.go", "new.go"}, paths(cs.Files))
	assert.Equal(t, map[string]string{"README.md": "M"}, otherPaths(cs.Others))
	for _, f := range cs.Files {
		assert.NotEmpty(t, f.Diff)
	}
}

func TestBuild_MatchNothingIsAnEmptyChangesetNotAnError(t *testing.T) {
	dir := initRepo(t)
	base := put(t, dir, "seed", map[string]string{"a.md": "1"})
	head := put(t, dir, "edit", map[string]string{"a.md": "2"})
	cs, err := Build(dir, rng(base, head), Options{Scan: scan, Select: func(Scope) (bool, error) { return false, nil }})
	require.NoError(t, err)
	assert.Empty(t, cs.Files)
	assert.Len(t, cs.Others, 1)
}

func TestBuild_AnEmptyRangeIsAnEmptyChangeset(t *testing.T) {
	dir := initRepo(t)
	head := put(t, dir, "seed", map[string]string{"a.md": "1"})
	cs, err := Build(dir, rng(head, head), Options{Scan: scan, Select: selectAll})
	require.NoError(t, err)
	assert.Empty(t, cs.Files)
	assert.Empty(t, cs.Commits)
}

// a10n #7: a diff that could not be computed read as an empty batch, and the
// base was promoted over a bad draft.
func TestBuild_AFailedDiffIsAnErrorNeverAnEmptyChangeset(t *testing.T) {
	dir := initRepo(t)
	head := put(t, dir, "seed", map[string]string{"a.md": "1"})
	cs, err := Build(dir, rng("0123456789012345678901234567890123456789", head), Options{Scan: scan, Select: selectAll})
	require.Error(t, err)
	assert.Equal(t, Changeset{}, cs)
}

func TestBuild_ASelectErrorFailsClosed(t *testing.T) {
	dir := initRepo(t)
	base := put(t, dir, "seed", map[string]string{"a.md": "1"})
	head := put(t, dir, "edit", map[string]string{"a.md": "2"})
	boom := errors.New("bad expression")
	_, err := Build(dir, rng(base, head), Options{Scan: scan, Select: func(Scope) (bool, error) { return false, boom }})
	assert.ErrorIs(t, err, boom)
}

func TestBuild_RequiresScanAndSelect(t *testing.T) {
	_, err := Build(t.TempDir(), rng("a", "b"), Options{})
	assert.Error(t, err)
}

func TestBuild_MarkersAndTheScopeMatchSees(t *testing.T) {
	dir := initRepo(t)
	base := put(t, dir, "seed", map[string]string{
		"lost.go": "// sr:invariant a.b\n",
		"kept.go": "// sr:invariant c.d\n",
	})
	git(t, dir, "rm", "-q", "lost.go")
	head := put(t, dir, "edit", map[string]string{"kept.go": "// nothing now\n", "gained.go": "// sr:proves x.y\n"})

	var scopes []Scope
	cs, err := Build(dir, rng(base, head), Options{Deletions: IncludeDeletions, Scan: scan, Select: func(s Scope) (bool, error) {
		scopes = append(scopes, s)
		return true, nil
	}})
	require.NoError(t, err)

	scope := map[string]Scope{}
	for _, s := range scopes {
		scope[s.Path] = s
	}
	assert.Equal(t, []Marker{{Kind: "invariant", FQN: "a.b", Line: 1}}, scope["lost.go"].Markers,
		"a deleted file is matched on the markers it carried")
	assert.Equal(t, "D", scope["lost.go"].Status)
	assert.Empty(t, scope["kept.go"].Markers)
	assert.Equal(t, []Marker{{Kind: "invariant", FQN: "c.d", Line: 1}}, scope["kept.go"].OldMarkers,
		"oldMarkers lets a rule see a write that removed a marker")
	assert.Equal(t, []Marker{{Kind: "proves", FQN: "x.y", Line: 1}}, scope["gained.go"].Markers)
	assert.Empty(t, scope["gained.go"].OldMarkers)

	assert.NotNil(t, fileNamed(t, cs, "kept.go").NewMarkers, "markers are lists, never null")
	assert.Equal(t, []Marker{{Kind: "invariant", FQN: "c.d", Line: 1}}, fileNamed(t, cs, "kept.go").OldMarkers)
}

func TestBuild_MatchSeesTheRangesTrailers(t *testing.T) {
	dir := initRepo(t)
	base := put(t, dir, "seed", map[string]string{"a.go": "1"})
	put(t, dir, "one\n\nSloprail-Refactor: move-only", map[string]string{"a.go": "2"})
	head := put(t, dir, "two\n\nsloprail-refactor: other\nSloprail-Cites-User: a quote", map[string]string{"a.go": "3"})

	var got map[string][]string
	cs, err := Build(dir, rng(base, head), Options{Scan: scan, Select: func(s Scope) (bool, error) {
		got = s.Trailers
		return true, nil
	}})
	require.NoError(t, err)
	assert.Equal(t, []string{"move-only", "other"}, got["Sloprail-Refactor"], "across the range's commits, in order, keys in canonical case")
	assert.Equal(t, []string{"a quote"}, got["Sloprail-Cites-User"])
	assert.Equal(t, []string{"a quote"}, cs.Commits[1].Trailers["Sloprail-Cites-User"])
}

func TestBuild_GitlinkChangesAreListedWithoutContent(t *testing.T) {
	dir := initRepo(t)
	base := put(t, dir, "seed", map[string]string{"a.txt": "1"})
	// A gitlink recorded straight in the index, with no submodule behind it.
	git(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+base+",vendor/lib")
	git(t, dir, "commit", "-qm", "add submodule pointer")
	head := git(t, dir, "rev-parse", "HEAD")

	cs, err := Build(dir, rng(base, head), Options{Scan: scan, Select: selectAll})
	require.NoError(t, err)
	f := fileNamed(t, cs, "vendor/lib")
	assert.Equal(t, "A", f.Status)
	assert.Empty(t, f.NewContent)
	assert.Contains(t, f.Diff, "Subproject commit")
}

func TestBuild_ARenameIsSelectedWhenTheOldPathIsGuarded(t *testing.T) {
	dir := initRepo(t)
	base := put(t, dir, "seed", map[string]string{"memories/x.md": "a long enough body to be seen as a rename\nsecond line\n"})
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "archive"), 0o755))
	git(t, dir, "mv", "memories/x.md", "archive/x.md")
	git(t, dir, "commit", "-m", "archive it")
	head := git(t, dir, "rev-parse", "HEAD")

	guardMemories := func(s Scope) (bool, error) { return len(s.Path) >= 9 && s.Path[:9] == "memories/", nil }
	cs, err := Build(dir, rng(base, head), Options{Scan: scan, Select: guardMemories})
	require.NoError(t, err)
	require.Equal(t, []string{"archive/x.md"}, paths(cs.Files), "moving a file out of a guarded path is a change to it")
	assert.Equal(t, "memories/x.md", cs.Files[0].OldPath)
	assert.Equal(t, "R", cs.Files[0].Status)

	// The new path alone is not guarded, and a plain addition there is not selected.
	other, err := Build(dir, rng(base, head), Options{Scan: scan, Select: func(s Scope) (bool, error) { return s.Path == "docs/x.md", nil }})
	require.NoError(t, err)
	assert.Empty(t, other.Files)
}

func TestBuild_ARenamesOldMarkersAreWhatTheOldPathIsMatchedOn(t *testing.T) {
	dir := initRepo(t)
	base := put(t, dir, "seed", map[string]string{"a/x.md": "sr:invariant Order.total\nbody line one\nbody line two\n"})
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "b"), 0o755))
	git(t, dir, "mv", "a/x.md", "b/x.md")
	git(t, dir, "commit", "-m", "move")
	head := git(t, dir, "rev-parse", "HEAD")

	var seen []Scope
	_, err := Build(dir, rng(base, head), Options{Scan: scan, Select: func(s Scope) (bool, error) {
		seen = append(seen, s)
		return false, nil
	}})
	require.NoError(t, err)
	require.Len(t, seen, 2, "asked on the new path, then on the old one")
	assert.Equal(t, "b/x.md", seen[0].Path)
	assert.Equal(t, "a/x.md", seen[1].Path)
	assert.Equal(t, "R", seen[1].Status)
	require.Len(t, seen[1].Markers, 1)
	assert.Equal(t, "Order.total", seen[1].Markers[0].FQN, "the old path is matched on the markers it carried")
}

func TestBuild_EachFileKnowsTheCommitsThatChangedIt(t *testing.T) {
	dir := initRepo(t)
	base := put(t, dir, "seed", map[string]string{"a.md": "a\n", "b.md": "b\n"})
	c1 := put(t, dir, "touch a", map[string]string{"a.md": "a1\n"})
	c2 := put(t, dir, "touch b", map[string]string{"b.md": "b1\n"})
	c3 := put(t, dir, "touch a again", map[string]string{"a.md": "a2\n"})

	cs, err := Build(dir, rng(base, c3), Options{Scan: scan, Select: selectAll})
	require.NoError(t, err)
	assert.Equal(t, []string{c1, c3}, fileNamed(t, cs, "a.md").Commits)
	assert.Equal(t, []string{c2}, fileNamed(t, cs, "b.md").Commits)
}
