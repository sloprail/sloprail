package gitrepo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withOrigin(t *testing.T) (dir, base string) {
	t.Helper()
	dir = initRepo(t)
	base = commitFile(t, dir, "a.md", "base\n", "seed")
	git(t, dir, "update-ref", "refs/remotes/origin/main", base)
	return dir, base
}

func TestRestoresRemoteBase(t *testing.T) {
	dir, _ := withOrigin(t)
	commitFile(t, dir, "a.md", "changed\n", "change")
	head := commitFile(t, dir, "a.md", "base\n", "restore")
	assert.True(t, RestoresRemoteBase(dir, head, "a.md"), "byte-identical to the base")

	partial := commitFile(t, dir, "a.md", "base\nplus\n", "partial")
	assert.False(t, RestoresRemoteBase(dir, partial, "a.md"))

	assert.False(t, RestoresRemoteBase(dir, head, "new.md"), "a file the base lacks is not a restore")
}

func TestRestoresRemoteBase_ModeDiffers(t *testing.T) {
	dir, _ := withOrigin(t)
	require.NoError(t, os.Chmod(filepath.Join(dir, "a.md"), 0o755))
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "chmod")
	head := git(t, dir, "rev-parse", "HEAD")
	assert.False(t, RestoresRemoteBase(dir, head, "a.md"), "same bytes, different mode")
}

func TestRestoresRemoteBase_NoRemoteOrNoSharedHistory(t *testing.T) {
	dir := initRepo(t)
	head := commitFile(t, dir, "a.md", "base\n", "seed")
	assert.False(t, RestoresRemoteBase(dir, head, "a.md"), "no origin at all")

	// An origin ref with unrelated history (what a shallow or foreign fetch leaves).
	other := initRepo(t)
	foreign := commitFile(t, other, "a.md", "base\n", "foreign")
	git(t, dir, "fetch", other, foreign)
	git(t, dir, "update-ref", "refs/remotes/origin/main", foreign)
	assert.False(t, RestoresRemoteBase(dir, head, "a.md"), "no shared history, no merge base")
}
