package gitrepo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
	f, err := os.OpenFile(filepath.Join(common, worktreeLockFile), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("gitrepo: worktree lock: %w", err)
	}
	defer f.Close()
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("gitrepo: worktree lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
