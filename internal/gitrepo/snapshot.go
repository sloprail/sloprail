package gitrepo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// addWorktree runs `git worktree add` under the repository's worktree lock. A failed attempt
// is cleaned up (half-made directory, its registration) before the error, which carries git's
// stderr, is returned.
//
// Only the registration is made under the lock (`--no-checkout`, a few milliseconds): writing
// the files is the dear step, and it needs no lock, since nothing else touches a registration
// that is not its own. So snapshots made at once are populated side by side, not one after another.
func addWorktree(dir, path, commit string) error {
	undo := func() {
		_ = os.RemoveAll(path)
		_, _ = run(dir, "worktree", "prune")
	}
	err := withWorktreeLock(dir, func() error {
		_, err := run(dir, "worktree", "add", "--detach", "--force", "--no-checkout", path, commit)
		if err != nil {
			undo()
		}
		return err
	})
	if err != nil {
		return err
	}
	// The checkout `worktree add` would have made: the commit's files, with the attribute filters.
	if _, err := run(path, "read-tree", "--reset", "-u", "HEAD"); err != nil {
		_ = withWorktreeLock(dir, func() error { undo(); return nil })
		return err
	}
	return nil
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
	// The files go first, outside the lock (deleting a tree is the dear step and touches nothing
	// shared); then the registration, which the prune finds dangling, under it.
	if err := os.RemoveAll(s.root); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, withWorktreeLock(s.repo, func() error {
		_, err := run(s.repo, "worktree", "prune")
		return err
	}))
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
