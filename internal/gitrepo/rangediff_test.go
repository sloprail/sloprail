package gitrepo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func byPath(ds []Delta) map[string]Delta {
	m := map[string]Delta{}
	for _, d := range ds {
		m[d.Path] = d
	}
	return m
}

func TestDeltas_AddModifyDeleteRename(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "mod.txt", "one\n")
	commit(t, dir, "del.txt", "gone\n")
	commit(t, dir, "mv.txt", "a long enough body that similarity detection keeps it\nline two\nline three\n")
	base := git(t, dir, "rev-parse", "HEAD")

	commit(t, dir, "mod.txt", "one\ntwo\n")
	commit(t, dir, "add.txt", "new\n")
	git(t, dir, "rm", "-q", "del.txt")
	git(t, dir, "mv", "mv.txt", "moved.txt")
	git(t, dir, "commit", "-qm", "rm and mv")
	head := git(t, dir, "rev-parse", "HEAD")

	ds, err := Deltas(dir, base, head)
	require.NoError(t, err)
	m := byPath(ds)
	assert.Equal(t, byte('M'), m["mod.txt"].Status)
	assert.Equal(t, byte('A'), m["add.txt"].Status)
	assert.Equal(t, byte('D'), m["del.txt"].Status)
	assert.Equal(t, byte('R'), m["moved.txt"].Status)
	assert.Equal(t, "mv.txt", m["moved.txt"].OldPath)
	assert.NotContains(t, m, "mv.txt", "a rename is one entry, not a deletion plus an addition")
	assert.Len(t, ds, 4)
}

func TestDeltas_IsTheSquashedNetDiff(t *testing.T) {
	// C1 deletes, C2 restores: the net range shows no change to the file.
	dir := initRepo(t)
	commit(t, dir, "keep.txt", "keep\n")
	base := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "rm", "-q", "keep.txt")
	git(t, dir, "commit", "-qm", "c1 delete")
	commit(t, dir, "keep.txt", "keep\n")
	head := git(t, dir, "rev-parse", "HEAD")

	ds, err := Deltas(dir, base, head)
	require.NoError(t, err)
	assert.Empty(t, ds)
}

func TestDeltas_EmptyRangeIsEmptyNotAnError(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "x")
	ds, err := Deltas(dir, head, head)
	require.NoError(t, err)
	assert.Empty(t, ds)
}

// A failed diff must not look like an empty one (a10n #7).
func TestDeltas_AnUnknownCommitIsAnError(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "x")
	ds, err := Deltas(dir, "0123456789012345678901234567890123456789", head)
	require.Error(t, err)
	assert.Nil(t, ds)
}

func TestDeltas_PathsWithSpacesAndUnicode(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "seed.txt", "s")
	commit(t, dir, "with space é.txt", "x")
	head := git(t, dir, "rev-parse", "HEAD")
	ds, err := Deltas(dir, base, head)
	require.NoError(t, err)
	require.Len(t, ds, 1)
	assert.Equal(t, "with space é.txt", ds[0].Path)
}

func TestDeltas_TypeChangeReadsAsModified(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "f", "x")
	base := git(t, dir, "rev-parse", "HEAD")
	require.NoError(t, os.Remove(filepath.Join(dir, "f")))
	require.NoError(t, os.Symlink("target", filepath.Join(dir, "f")))
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "type change")
	ds, err := Deltas(dir, base, git(t, dir, "rev-parse", "HEAD"))
	require.NoError(t, err)
	require.Len(t, ds, 1)
	assert.Equal(t, byte('M'), ds[0].Status)
}

func TestParseRaw_RefusesWhatItDoesNotUnderstand(t *testing.T) {
	sha := "0000000000000000000000000000000000000000"
	for name, in := range map[string]string{
		"unknown status":     ":100644 100644 " + sha + " " + sha + " U\x00a\x00",
		"copy is not listed": ":100644 100644 " + sha + " " + sha + " C100\x00a\x00b\x00",
		"missing path":       ":100644 100644 " + sha + " " + sha + " M\x00",
		"rename one path":    ":100644 100644 " + sha + " " + sha + " R100\x00a\x00",
		"not a header":       "garbage\x00a\x00",
		"empty path":         ":100644 100644 " + sha + " " + sha + " M\x00\x00",
	} {
		_, err := parseRaw(in)
		assert.Error(t, err, name)
	}
}

func TestPatchOf_RenameShowsAsARename(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "a.txt", "line one\nline two\nline three\nline four\n")
	base := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "mv", "a.txt", "b.txt")
	git(t, dir, "commit", "-qm", "mv")
	head := git(t, dir, "rev-parse", "HEAD")

	ds, err := Deltas(dir, base, head)
	require.NoError(t, err)
	require.Len(t, ds, 1)
	p, err := PatchOf(dir, base, head, ds[0])
	require.NoError(t, err)
	assert.Contains(t, p, "rename from a.txt")
	assert.Contains(t, p, "rename to b.txt")
}

func TestPatchOf_ModificationHunk(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "a.txt", "one\n")
	base := git(t, dir, "rev-parse", "HEAD")
	head := commit(t, dir, "a.txt", "one\ntwo\n")
	p, err := PatchOf(dir, base, head, Delta{Path: "a.txt", Status: 'M'})
	require.NoError(t, err)
	assert.Contains(t, p, "+two")
}

func TestBlobAt_ReadsTheCommitNotTheWorkingTree(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "committed\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("dirty\n"), 0o644))

	got, err := BlobAt(dir, head, "a.txt")
	require.NoError(t, err)
	assert.Equal(t, "committed\n", got)
}

func TestBlobAt_EmptyBlobIsEmptyMissingBlobIsAnError(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "empty.txt", "")
	got, err := BlobAt(dir, head, "empty.txt")
	require.NoError(t, err)
	assert.Equal(t, "", got)

	_, err = BlobAt(dir, head, "absent.txt")
	assert.Error(t, err)
}
