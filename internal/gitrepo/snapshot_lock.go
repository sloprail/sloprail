package gitrepo

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// worktreeLockFile lives in the repository's common git directory, so every process working on
// the repository (a Stop and a sub-agent's Stop, two sessions) and every linked worktree of it
// agree on one lock.
const worktreeLockFile = "sloprail-worktree.lock"

// withWorktreeLock runs fn while holding an exclusive advisory lock on dir's repository.
//
// `git worktree add`, `remove` and `prune` share .git/worktrees without a lock of their own
// that covers the whole of each: a prune running while another process is between creating a
// registration and finishing it deletes that registration (a name that is then free again, so
// a third add reuses it), and the adds and removes end up with each other's registrations or
// with none ("is not a working tree"). Serializing the three makes each see a settled registry.
func withWorktreeLock(dir string, fn func() error) error {
	out, err := run(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	common := strings.TrimSpace(out)
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	path := filepath.Join(common, worktreeLockFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("gitrepo: worktree lock: %w", err)
	}
	defer f.Close()
	if err := acquireLock(f, path); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	// The holder's pid, for the message of whoever times out waiting.
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return fn()
}

// worktreeLockWait bounds how long a caller waits for the lock. Each critical section is a
// few git calls, so a wait this long means a holder is stuck, and that is reported, not waited out.
var worktreeLockWait = 2 * time.Minute

// acquireLock polls a non-blocking flock until worktreeLockWait passes.
func acquireLock(f *os.File, path string) error {
	deadline := time.Now().Add(worktreeLockWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EINTR {
			return fmt.Errorf("gitrepo: worktree lock %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			holder, _ := os.ReadFile(path)
			return fmt.Errorf("gitrepo: worktree lock %s still held after %s by pid %s",
				path, worktreeLockWait, strings.TrimSpace(string(holder)))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
