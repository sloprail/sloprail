package gitrepo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSnapshot_HoldsTheCommitNotTheDirtyTree(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "committed\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("dirty\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("x"), 0o644))

	s, err := AddSnapshot(dir, t.TempDir(), head)
	require.NoError(t, err)
	t.Cleanup(func() { s.Remove() })

	got, err := os.ReadFile(filepath.Join(s.Path, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "committed\n", string(got))
	assert.NoFileExists(t, filepath.Join(s.Path, "scratch.txt"))
}

func TestSnapshot_IsReadOnly(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "x")
	s, err := AddSnapshot(dir, t.TempDir(), head)
	require.NoError(t, err)
	t.Cleanup(func() { s.Remove() })

	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	assert.Error(t, os.WriteFile(filepath.Join(s.Path, "a.txt"), []byte("y"), 0o644))
	assert.Error(t, os.WriteFile(filepath.Join(s.Path, "new.txt"), []byte("y"), 0o644))
	assert.Error(t, os.Remove(filepath.Join(s.Path, "a.txt")))
}

func TestSnapshot_RemoveDeletesItAndItsRegistration(t *testing.T) {
	dir := initRepo(t)
	head := commitIn(t, dir, "sub/a.txt", "x")
	parent := t.TempDir()
	s, err := AddSnapshot(dir, parent, head)
	require.NoError(t, err)
	require.DirExists(t, s.Path)
	assert.Contains(t, git(t, dir, "worktree", "list"), s.Path)

	require.NoError(t, s.Remove())
	assert.NoDirExists(t, s.Path)
	entries, _ := os.ReadDir(parent)
	assert.Empty(t, entries)
	assert.NotContains(t, git(t, dir, "worktree", "list"), s.Path)
	assert.NoError(t, s.Remove(), "removing twice is harmless")
}

func TestSnapshot_DoesNotDisturbTheWorkingTree(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "x")
	before := git(t, dir, "status", "--porcelain")
	s, err := AddSnapshot(dir, t.TempDir(), head)
	require.NoError(t, err)
	require.NoError(t, s.Remove())
	assert.Equal(t, before, git(t, dir, "status", "--porcelain"))
	assert.Equal(t, head, git(t, dir, "rev-parse", "HEAD"))
}

func TestSnapshot_ARefNameIsRefused(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "a.txt", "x")
	_, err := AddSnapshot(dir, t.TempDir(), "main")
	assert.Error(t, err, "snapshots are of SHAs, never names")
}

func TestSnapshot_AnUnknownCommitIsAnErrorAndLeavesNothingBehind(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "a.txt", "x")
	parent := t.TempDir()
	_, err := AddSnapshot(dir, parent, "0123456789012345678901234567890123456789")
	require.Error(t, err)
	entries, _ := os.ReadDir(parent)
	assert.Empty(t, entries)
}
