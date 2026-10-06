package gitrepo

import (
	"os"
	"strings"
)

// BranchWorktree is the path of ANOTHER worktree of the repository that has branch checked out
// (not dir itself), or "" when none does.
func BranchWorktree(dir, branch string) string {
	if branch == "" {
		return ""
	}
	out, err := run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	self, _ := run(dir, "rev-parse", "--show-toplevel")
	self = strings.TrimSpace(self)
	var path string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case line == "branch refs/heads/"+branch:
			if path != "" && !sameFile(path, self) {
				return path
			}
		}
	}
	return ""
}

func sameFile(a, b string) bool {
	ai, err1 := os.Stat(a)
	bi, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ai, bi)
}

// RemoteDefaultBase is the merge base of head with the REMOTE default branch (origin's HEAD, else
// origin/main, origin/master): what is already on it is not work still to answer for. ok is false
// when there is no such branch or no shared history; a local main is never taken for it.
func RemoteDefaultBase(dir, head string) (sha string, ok bool) {
	for _, c := range remoteDefaultCandidates(dir) {
		if _, err := commitOf(dir, c, "--base"); err != nil {
			continue
		}
		out, err := run(dir, "merge-base", c, head)
		if err != nil {
			continue
		}
		if mb := strings.TrimSpace(out); isObjectName(mb) {
			return mb, true
		}
	}
	return "", false
}

// RemoteDefaultTip is the commit the remote default branch stands at; ok is false without one.
func RemoteDefaultTip(dir string) (sha string, ok bool) {
	for _, c := range remoteDefaultCandidates(dir) {
		if tip, err := commitOf(dir, c, "--base"); err == nil {
			return tip, true
		}
	}
	return "", false
}

func remoteDefaultCandidates(dir string) []string {
	candidates := []string{}
	if out, err := run(dir, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if ref := strings.TrimSpace(out); ref != "" {
			candidates = append(candidates, ref)
		}
	}
	return append(candidates, "origin/main", "origin/master")
}

// PinRef points ref at the commit sha, so garbage collection keeps it after the branch that held
// it is deleted.
func PinRef(dir, ref, sha string) error {
	_, err := run(dir, "update-ref", ref, sha)
	return err
}

// RestoresRemoteBase reports whether path at head is byte-identical, mode included, to path at
// the merge base of head with the remote default branch. False when there is no such branch (no
// origin, no shared history), the base lacks the file, or anything cannot be read: a restore is
// then never assumed. The remote-tracking ref is local state a user can move; CI's verify,
// which judges the squashed range, is the guarantee.
func RestoresRemoteBase(dir, head, path string) bool {
	base, ok := RemoteDefaultBase(dir, head)
	if !ok {
		return false
	}
	entry := func(rev string) string {
		out, err := run(dir, "ls-tree", "-z", rev, "--", path)
		if err != nil || strings.TrimSpace(out) == "" {
			return ""
		}
		meta, _, _ := strings.Cut(out, "\t") // "<mode> <type> <oid>"
		return meta
	}
	a, b := entry(base), entry(head)
	return a != "" && a == b
}
