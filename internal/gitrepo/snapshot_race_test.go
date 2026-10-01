package gitrepo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Snapshots made at the same moment by different processes (a Stop and a sub-agent's Stop,
// two sessions on one repository) must not fight over the worktree's registration name.
func TestSnapshot_ConcurrentSnapshotsDoNotCollide(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "x")
	parent := t.TempDir()

	const n = 24
	errs := make(chan error, n)
	snaps := make(chan *Snapshot, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			<-start
			s, err := AddSnapshot(dir, parent, head)
			errs <- err
			snaps <- s
		}()
	}
	close(start)
	for i := 0; i < n; i++ {
		assert.NoError(t, <-errs)
	}
	for i := 0; i < n; i++ {
		if s := <-snaps; s != nil {
			assert.NoError(t, s.Remove())
		}
	}
}

// Two live snapshots never share a registration name (the checkout directory's base name).
func TestSnapshot_NamesAreUnique(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "x")
	a, err := AddSnapshot(dir, t.TempDir(), head)
	require.NoError(t, err)
	b, err := AddSnapshot(dir, t.TempDir(), head)
	require.NoError(t, err)
	t.Cleanup(func() { a.Remove(); b.Remove() })
	assert.NotEqual(t, filepath.Base(a.Path), filepath.Base(b.Path))
}

// A registration left by a process that died (its directory gone) must not stand in the way.
func TestSnapshot_StaleRegistrationIsPruned(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "x")
	gone := filepath.Join(t.TempDir(), "gone")
	git(t, dir, "worktree", "add", "--detach", gone, head)
	require.NoError(t, os.RemoveAll(gone))

	s, err := AddSnapshot(dir, t.TempDir(), head)
	require.NoError(t, err)
	require.NoError(t, s.Remove())
	assert.NotContains(t, git(t, dir, "worktree", "list"), gone)
}
