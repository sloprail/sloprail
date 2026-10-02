//go:build !windows

package checkstore

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// A Stop holds a SHARED advisory lock for as long as it evaluates an old per-session database;
// a migration takes the EXCLUSIVE lock, without waiting, before it touches the same database.
// The kernel releases a lock when its process dies, so a killed Stop never leaves one behind
// (which a "running" row in the database cannot say). The lock file lives in the repository
// database's directory, keyed by the source path, so the old files and directories stay
// untouched.

type lockState int

const (
	lockAcquired lockState = iota
	// lockHeld: another process holds it — a Stop is evaluating.
	lockHeld
	// lockUnavailable: no lock could be made (the file could not be created or opened). That
	// says nothing about a Stop; the caller falls back to the RUNNING-row check alone.
	lockUnavailable
)

func flockOpen(path string, how int) (*os.File, lockState) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, lockUnavailable
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, lockUnavailable
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, lockHeld
		}
		return nil, lockUnavailable
	}
	return f, lockAcquired
}

// processGone says the process provably does not exist (never "cannot tell").
func processGone(pid int) bool {
	return pid > 0 && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func lockShared(path string) func() {
	f, st := flockOpen(path, syscall.LOCK_SH)
	if st != lockAcquired {
		return func() {}
	}
	return func() { f.Close() }
}

// tryExclusive takes the lock without waiting.
func tryExclusive(path string) (func(), lockState) {
	f, st := flockOpen(path, syscall.LOCK_EX|syscall.LOCK_NB)
	if st != lockAcquired {
		return func() {}, st
	}
	return func() { f.Close() }, lockAcquired
}
