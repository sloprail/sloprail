package checkcache

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// migrationLockFile lives in the repository's common git directory, so every process working
// on the repository (several sessions, linked worktrees) agrees on one lock.
const migrationLockFile = "sloprail-checks-migrate.lock"

// migrationLockWait bounds how long a migration waits for another process's. The holder is
// rewriting the records, which takes as long as the migration does; a wait this long means it
// is stuck, and the waiter then goes on without the lock, which is safe: the ref moves only by
// compare-and-swap, so one of the two commits is refused and retried against the other's.
const migrationLockWait = 15 * time.Minute

// lockMigration serializes key migrations across processes, so a second `sr-checks run that
// meets an older store waits for the first to finish (and then finds the current directory
// there and does nothing) instead of rebuilding every key a second time. Reading is never
// locked: a `verify` or `show` meanwhile reads the ref as it stands. It returns what releases
// the lock; a lock that cannot be taken is no reason not to migrate, so it never fails.
func (s *Store) lockMigration() func() {
	out, err := s.g.str("rev-parse", "--git-common-dir")
	if err != nil {
		return func() {}
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(s.g.dir, out)
	}
	f, err := os.OpenFile(filepath.Join(out, migrationLockFile), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return func() {}
	}
	deadline := time.Now().Add(migrationLockWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }
		}
		if err != syscall.EWOULDBLOCK || time.Now().After(deadline) {
			f.Close()
			return func() {}
		}
		time.Sleep(100 * time.Millisecond)
	}
}
