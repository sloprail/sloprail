package gitrepo

import (
	"os"
	"path/filepath"
	"strconv"
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
	lock := filepath.Join(dir, ".git", worktreeLockFile)
	require.NoError(t, os.Remove(lock))
	require.NoError(t, os.Mkdir(lock, 0o755))
	parent := t.TempDir()
	_, err = AddSnapshot(dir, parent, head)
	assert.Error(t, err)
	left, _ := os.ReadDir(parent)
	assert.Empty(t, left)

	root := s.root
	assert.Error(t, s.Remove())
	assert.NoDirExists(t, root)
}

// A holder that never lets go is reported by name after the bound, not waited for for ever.
func TestSnapshot_LockWaitIsBounded(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "x")
	old := worktreeLockWait
	worktreeLockWait = 300 * time.Millisecond
	t.Cleanup(func() { worktreeLockWait = old })

	require.NoError(t, withWorktreeLock(dir, func() error {
		_, err := AddSnapshot(dir, t.TempDir(), head)
		require.Error(t, err)
		assert.Contains(t, err.Error(), worktreeLockFile)
		assert.Contains(t, err.Error(), "still held after")
		assert.Contains(t, err.Error(), strconv.Itoa(os.Getpid()))
		return nil
	}))
}

// The sweep is housekeeping: with the lock busy it is skipped at once, adding no wait.
func TestSnapshot_SweepSkipsWhenLockBusy(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, withWorktreeLock(dir, func() error {
		start := time.Now()
		SweepStaleSnapshots(dir)
		// Far under worktreeLockWait (2m) a waiting sweep would take, and loose enough for the
		// one git call before the lock try on a loaded machine.
		assert.Less(t, time.Since(start), time.Minute)
		return nil
	}))
}

// 20 workers adding, sweeping and removing one repository finish in bounded time, none failing:
// the lock is never held while waiting for another lock.
func TestSnapshot_ParallelAddRemoveSweepIsBounded(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "x")
	done := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 20*2)
	for w := 0; w < 20; w++ {
		parent := t.TempDir()
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := AddSnapshot(dir, parent, head)
			errs <- err
			SweepStaleSnapshots(dir)
			if err == nil {
				errs <- s.Remove()
			}
		}()
	}
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(90 * time.Second):
		t.Fatal("parallel add/remove/sweep did not finish: lock deadlock")
	}
	close(errs)
	for err := range errs {
		assert.NoError(t, err)
	}
}
