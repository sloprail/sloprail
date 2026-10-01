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
	// The checkout's directory name is the worktree's registration name in git's metadata:
	// a fixed "tree" made every snapshot of every process contend for one name. The
	// temporary directory's own name is unique.
	s := &Snapshot{Path: filepath.Join(root, filepath.Base(root)), repo: dir, root: root}
	_, err = run(dir, "worktree", "add", "--detach", "--force", s.Path, commit)
	if err != nil {
		// A registration a dead process left behind (its directory gone), or a half-made one
		// from this very attempt, is what git trips over: prune, clear the attempt, once more.
		_, _ = run(dir, "worktree", "prune")
		_ = os.RemoveAll(s.Path)
		_, err = run(dir, "worktree", "add", "--detach", "--force", s.Path, commit)
	}
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
	return s, nil
}

// Remove deletes the snapshot and its worktree registration. Safe to call twice.
func (s *Snapshot) Remove() error {
	if s == nil || s.root == "" {
		return nil
	}
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
