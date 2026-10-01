package gitrepo

import (
	"path/filepath"
	"strings"
)

// Pins: hidden refs that keep a recorded commit alive.
//
// A tip the session committed on stays OWED a judgement until a rule passes it. Deleting
// its branch and running `git gc --prune=now` would make those commits unreachable and
// delete them, and nothing could then judge them. A pin is an ordinary ref under
// refs/sloprail/ (outside refs/heads, refs/remotes and refs/tags, so no branch listing,
// `Reachable` or `RefTips` sees it) that holds the commit until the tip is settled.

// PinPrefix is where every pin lives.
const PinPrefix = "refs/sloprail/pins/"

// UpdateRef points a full ref name at a commit.
func UpdateRef(dir, ref, sha string) error {
	_, err := run(dir, "update-ref", ref, sha)
	return err
}

// DeleteRef removes a ref; one that is not there is not an error.
func DeleteRef(dir, ref string) error {
	if sha, err := RefTip(dir, ref); err != nil || sha == "" {
		return err
	}
	_, err := run(dir, "update-ref", "-d", ref)
	return err
}

// LocalBranchesContaining names the local branches (full ref names) that contain commit.
func LocalBranchesContaining(dir, commit string) ([]string, error) {
	out, err := run(dir, "for-each-ref", "--format=%(refname)", "--contains", commit, "refs/heads")
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, line := range strings.Split(out, "\n") {
		if r := strings.TrimSpace(line); r != "" {
			refs = append(refs, r)
		}
	}
	return refs, nil
}

// CommitCarrying names (abbreviated SHA and subject) the newest commit on up that touches
// the files tip changed since it diverged from up: the squash commit that landed a branch.
// "" when there is none or it cannot be told.
func CommitCarrying(dir, up, tip string) string {
	if up == "" || tip == "" {
		return ""
	}
	mb, err := MergeBaseOf(dir, up, tip)
	if err != nil || mb == "" {
		return ""
	}
	out, err := run(dir, "diff", "--name-only", "-z", "--no-renames", mb, tip)
	if err != nil {
		return ""
	}
	args := []string{"log", "-1", "--format=%h %s", up, "--"}
	n := 0
	for _, p := range strings.Split(out, "\x00") {
		if p == "" {
			continue
		}
		if n++; n > 50 {
			break
		}
		args = append(args, p)
	}
	if n == 0 {
		return ""
	}
	log, err := run(dir, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(log)
}

// MergeBaseOf is the best common ancestor of two commits, or "" when they share none.
func MergeBaseOf(dir, a, b string) (string, error) {
	out, err := run(dir, "merge-base", a, b)
	if err != nil {
		if exitCode(err) == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// MainWorktree is the working tree that holds the repository's own .git directory, for a
// directory that is a linked worktree of it (or the directory itself when it is that
// tree), or "" when it cannot be told (a bare repository, an old git).
func MainWorktree(dir string) string {
	out, err := run(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return ""
	}
	common := strings.TrimSpace(out)
	if filepath.Base(common) != ".git" {
		return ""
	}
	home := filepath.Dir(common)
	if root, err := Root(home); err == nil && root != "" {
		return root
	}
	return home
}
