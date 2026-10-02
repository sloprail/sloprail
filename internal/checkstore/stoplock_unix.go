//go:build !windows

package checkstore

import (
	"os"
	"syscall"
)

// A Stop holds a SHARED advisory lock on `<checks.db>.stop-lock` for as long as it evaluates a
// database; a migration takes the EXCLUSIVE lock, without waiting, before it touches the same
// database. The kernel releases a lock when its process dies, so a killed Stop never leaves one
// behind (which a "running" row in the database cannot say).

func flockOpen(path string, how int) (*os.File, bool) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		return nil, false
	}
	return f, true
}

func lockShared(path string) func() {
	f, ok := flockOpen(path, syscall.LOCK_SH)
	if !ok {
		return func() {}
	}
	return func() { f.Close() }
}

// tryExclusive reports false when something holds the lock (a Stop is evaluating).
func tryExclusive(path string) (func(), bool) {
	f, ok := flockOpen(path, syscall.LOCK_EX|syscall.LOCK_NB)
	if !ok {
		return nil, false
	}
	return func() { f.Close() }, true
}
