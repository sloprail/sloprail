package gitrepo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func statusOf(t *testing.T, dir string) map[string]byte {
	t.Helper()
	cs, err := UncommittedChanges(dir)
	require.NoError(t, err)
	m := map[string]byte{}
	for _, c := range cs {
		m[c.Path] = c.Status
	}
	return m
}

func TestUncommittedChanges_CleanTreeHasNone(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "a.txt", "1")
	assert.Empty(t, statusOf(t, dir))
}

func TestUncommittedChanges_StagedUnstagedUntrackedAndDeleted(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "mod.txt", "1")
	commit(t, dir, "staged.txt", "1")
	commit(t, dir, "gone.txt", "1")
	commit(t, dir, "gone-staged.txt", "1")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "mod.txt"), []byte("2"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("2"), 0o644))
	git(t, dir, "add", "staged.txt")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fresh.txt"), []byte("n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "added.txt"), []byte("n"), 0o644))
	git(t, dir, "add", "added.txt")
	require.NoError(t, os.Remove(filepath.Join(dir, "gone.txt")))
	git(t, dir, "rm", "-q", "gone-staged.txt")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "deep.txt"), []byte("n"), 0o644))

	assert.Equal(t, map[string]byte{
		"mod.txt": 'M', "staged.txt": 'M', "fresh.txt": 'A', "added.txt": 'A',
		"gone.txt": 'D', "gone-staged.txt": 'D', "sub/deep.txt": 'A',
	}, statusOf(t, dir), "untracked files are listed one by one, not as their directory")
}

func TestUncommittedChanges_RenameCarriesItsOldPath(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "old.txt", "a long enough body that similarity keeps the rename\nline two\nline three\n")
	git(t, dir, "mv", "old.txt", "new.txt")
	cs, err := UncommittedChanges(dir)
	require.NoError(t, err)
	require.Len(t, cs, 1)
	assert.Equal(t, Uncommitted{Path: "new.txt", OldPath: "old.txt", Status: 'R'}, cs[0])
}

func TestUncommittedChanges_AddedThenDeletedDiffersInNothing(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "a.txt", "1")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tmp.txt"), []byte("x"), 0o644))
	git(t, dir, "add", "tmp.txt")
	require.NoError(t, os.Remove(filepath.Join(dir, "tmp.txt")))
	assert.Empty(t, statusOf(t, dir))
}

func TestUncommittedChanges_IgnoredFilesAreNotListed(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, ".gitignore", "*.log\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.log"), []byte("x"), 0o644))
	assert.Empty(t, statusOf(t, dir))
}

func TestUncommittedChanges_PathsWithSpaces(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "a.txt", "1")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "with space é.txt"), []byte("x"), 0o644))
	assert.Equal(t, map[string]byte{"with space é.txt": 'A'}, statusOf(t, dir))
}

func TestUncommittedChanges_AGitErrorIsAnErrorNotACleanTree(t *testing.T) {
	_, err := UncommittedChanges(t.TempDir())
	assert.ErrorIs(t, err, ErrNotARepository)

	dir := initRepo(t)
	commit(t, dir, "a.txt", "1")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("garbage\n"), 0o644))
	_, err = UncommittedChanges(dir)
	assert.Error(t, err)
}

func TestUncommittedChanges_AnUntrackedNestedRepositoryIsNotListedButATrackedOneIs(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "a.txt", "1")

	// A clone the agent made to look at: untracked, so not this repository's.
	nested := filepath.Join(dir, "vendor", "clone")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	git(t, nested, "init", "-q", "--initial-branch=main")
	git(t, nested, "config", "user.email", "t@example.invalid")
	git(t, nested, "config", "user.name", "T")
	require.NoError(t, os.WriteFile(filepath.Join(nested, "f.txt"), []byte("x"), 0o644))
	git(t, nested, "add", ".")
	git(t, nested, "commit", "-qm", "inner")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "vendor", "plain.txt"), []byte("p"), 0o644))

	assert.Equal(t, map[string]byte{"vendor/plain.txt": 'A'}, statusOf(t, dir),
		"the nested repository is left out; its neighbours are not")

	// Once it is tracked (a gitlink), it is this repository's, and counts.
	git(t, dir, "add", "vendor/clone")
	assert.Equal(t, byte('A'), statusOf(t, dir)["vendor/clone"])
}
