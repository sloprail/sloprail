package gitrepo

import (
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Snapshot is a read-only checkout of one commit, for checks to read.
//
// A file-guard judges commits, so its checks must not see the working tree: a
// half-finished edit is not what was committed. The snapshot is a detached
// worktree of the commit, made read-only so a check cannot write into what it is
// judging, and it is removed when the caller is done.
type Snapshot struct {
	// Path is the root of the checkout.
	Path string

	repo string
	root string // the temporary directory that holds Path
}

// AddSnapshot checks commit out, detached, under a new directory in parent
// ("" = the system temporary directory).
func AddSnapshot(dir, parent, commit string) (*Snapshot, error) {
	if !isObjectName(commit) {
		return nil, fmt.Errorf("gitrepo: snapshot wants a commit SHA, not %q", commit)
	}
	root, err := os.MkdirTemp(parent, "sr-tree-")
	if err != nil {
		return nil, fmt.Errorf("gitrepo: snapshot directory: %w", err)
	}
	// The directory is named "tree" under the unique temporary root: callers (and the judge's
	// --add-dir) rely on that shape. Git numbers colliding registration names itself.
	s := &Snapshot{Path: filepath.Join(root, "tree"), repo: dir, root: root}
	if err := writeOwner(root); err != nil {
		os.RemoveAll(root)
		return nil, fmt.Errorf("gitrepo: snapshot owner: %w", err)
	}
	// Snapshots a killed run left behind (their owner is dead) would otherwise stay registered
	// as read-only detached worktrees for ever.
	SweepStaleSnapshots(dir)
	err = addWorktree(dir, s.Path, commit)
	if err != nil {
		os.RemoveAll(root)
		_, _ = run(dir, "worktree", "prune") // the failed attempt must not leave its registration
		return nil, fmt.Errorf("gitrepo: snapshot of %s: %w", short(commit), err)
	}
	if err := setWritable(s.Path, false); err != nil {
		// A snapshot that could not be locked is not the read-only one the check
		// was promised; tear it down rather than hand it over.
		return nil, errors.Join(fmt.Errorf("gitrepo: make snapshot read-only: %w", err), s.Remove())
	}
	track(s)
	return s, nil
}

// worktreeAddTries and the backoff bound how long concurrent `git worktree add` calls on one
// repository (they share .git/worktrees and its locks) are retried: about ten seconds in all.
const (
	worktreeAddTries   = 8
	worktreeAddBackoff = 100 * time.Millisecond
	worktreeAddCeiling = 2 * time.Second
)

// addWorktree runs `git worktree add`, retrying with jittered, doubling backoff while git fails
// with exit 128 (a collision on the shared registration or a lock). Between tries it prunes a
// registration a dead process left behind and clears the half-made attempt. The last error
// carries git's stderr.
func addWorktree(dir, path, commit string) error {
	var err error
	delay := worktreeAddBackoff
	for try := 0; try < worktreeAddTries; try++ {
		if _, err = run(dir, "worktree", "add", "--detach", "--force", path, commit); err == nil {
			return nil
		}
		if !retryableWorktreeAdd(err) {
			return err
		}
		_, _ = run(dir, "worktree", "prune")
		_ = os.RemoveAll(path)
		if try == worktreeAddTries-1 {
			break
		}
		time.Sleep(delay/2 + time.Duration(rand.Int63n(int64(delay))))
		if delay *= 2; delay > worktreeAddCeiling {
			delay = worktreeAddCeiling
		}
	}
	return err
}

// retryableWorktreeAdd is true for git's exit 128 (its fatal error), which is how lock and
// registration collisions ("could not lock", "File exists", "is locked") surface.
func retryableWorktreeAdd(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 128
}

// Remove deletes the snapshot and its worktree registration. Safe to call twice.
func (s *Snapshot) Remove() error {
	if s == nil || s.root == "" {
		return nil
	}
	untrack(s)
	var errs []error
	if err := setWritable(s.Path, true); err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, err)
	}
	if _, err := run(s.repo, "worktree", "remove", "--force", s.Path); err != nil {
		// Already gone is fine; the directory removal and prune below finish the job.
		errs = append(errs, err)
	}
	if err := os.RemoveAll(s.root); err != nil {
		errs = append(errs, err)
	}
	if _, err := run(s.repo, "worktree", "prune"); err != nil {
		errs = append(errs, err)
	}
	s.root = ""
	return errors.Join(errs...)
}

// setWritable flips the write bits on everything under root. Directories go
// first when unlocking and last when locking, so the walk can always descend.
func setWritable(root string, writable bool) error {
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink == 0 {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !writable {
		for i, j := 0, len(paths)-1; i < j; i, j = i+1, j-1 {
			paths[i], paths[j] = paths[j], paths[i]
		}
	}
	for _, p := range paths {
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if writable {
			mode |= 0o200
		} else {
			mode &^= 0o222
		}
		if err := os.Chmod(p, mode); err != nil {
			return err
		}
	}
	return nil
}
