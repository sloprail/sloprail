package gitrepo

import (
	"errors"
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

// The checkout is always `<unique root>/tree` (the judge's --add-dir depends on it), and
// two live snapshots of one commit coexist.
func TestSnapshot_TwoLiveSnapshotsKeepTheTreeShape(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "x")
	a, err := AddSnapshot(dir, t.TempDir(), head)
	require.NoError(t, err)
	b, err := AddSnapshot(dir, t.TempDir(), head)
	require.NoError(t, err)
	t.Cleanup(func() { a.Remove(); b.Remove() })
	assert.Equal(t, "tree", filepath.Base(a.Path))
	assert.Equal(t, "tree", filepath.Base(b.Path))
	assert.NotEqual(t, a.Path, b.Path)
	assert.DirExists(t, a.Path)
	assert.DirExists(t, b.Path)
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

// Twenty parallel agents snapshotting one repository (the shared .git/worktrees and its locks)
// all get their snapshot: a collision is retried, not reported.
func TestSnapshot_TwentyParallelSnapshotsOfOneRepoAllSucceed(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "x")

	const n = 20
	type result struct {
		s   *Snapshot
		err error
	}
	results := make(chan result, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			<-start
			s, err := AddSnapshot(dir, t.TempDir(), head)
			results <- result{s, err}
		}()
	}
	close(start)
	for i := 0; i < n; i++ {
		r := <-results
		require.NoError(t, r.err)
		assert.NoError(t, r.s.Remove())
	}
}

// Only git's fatal exit (128) is worth retrying; any other failure is final.
func TestRetryableWorktreeAdd(t *testing.T) {
	assert.False(t, retryableWorktreeAdd(errors.New("boom")))
}
