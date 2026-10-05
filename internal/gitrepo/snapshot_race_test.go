package gitrepo

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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
// all get their snapshot: `git worktree add/remove/prune` on one repository are serialized, so
// none of them prunes or renames another's registration mid-flight.
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

// Adds overlapping removes and sweeps (each of which prunes) all succeed: this is the
// interleaving that deleted another snapshot's half-made registration.
func TestSnapshot_AddsOverlapRemovesAndSweeps(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "x")

	const workers, rounds = 8, 4
	var wg sync.WaitGroup
	errs := make(chan error, workers*rounds*2)
	for w := 0; w < workers; w++ {
		parent := t.TempDir()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				s, err := AddSnapshot(dir, parent, head)
				errs <- err
				if err == nil {
					errs <- s.Remove()
				}
				SweepStaleSnapshots(dir)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		assert.NoError(t, err)
	}
}

// The worktree lock is exclusive: while one holder has it, a snapshot waits.
func TestSnapshot_WaitsForTheWorktreeLock(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "x")

	done := make(chan *Snapshot, 1)
	parent := t.TempDir()
	require.NoError(t, withWorktreeLock(dir, func() error {
		go func() {
			s, _ := AddSnapshot(dir, parent, head)
			done <- s
		}()
		select {
		case <-done:
			t.Error("snapshot was made while the lock was held")
		case <-time.After(500 * time.Millisecond):
		}
		return nil
	}))
	s := <-done
	require.NotNil(t, s)
	assert.NoError(t, s.Remove())
}

// When the lock cannot be taken, no snapshot is made, and Remove still deletes its root.
func TestSnapshot_LockFailureLeavesNothingBehind(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "x")
	s, err := AddSnapshot(dir, t.TempDir(), head)
	require.NoError(t, err)

	// A directory where the lock file belongs makes opening it fail.
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git", worktreeLockFile), 0o755))
	parent := t.TempDir()
	_, err = AddSnapshot(dir, parent, head)
	assert.Error(t, err)
	left, _ := os.ReadDir(parent)
	assert.Empty(t, left)

	root := s.root
	assert.Error(t, s.Remove())
	assert.NoDirExists(t, root)
}
