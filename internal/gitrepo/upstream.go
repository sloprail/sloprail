package gitrepo

import (
	"strings"
)

// DefaultRemoteRef is the local remote-tracking ref of the repository's default
// branch, or "" when none is known: origin/HEAD (any remote's HEAD) first, else the
// upstream of the local main or master. Nothing is fetched; only refs already in the
// repository are read.
func DefaultRemoteRef(dir string) string {
	if out, err := run(dir, "for-each-ref", "--format=%(refname)", "refs/remotes/*/HEAD"); err == nil {
		for _, head := range strings.Fields(out) {
			ref, err := run(dir, "symbolic-ref", "-q", head)
			if err != nil {
				continue
			}
			if ref = strings.TrimSpace(ref); ref != "" && refExists(dir, ref) {
				return ref
			}
		}
	}
	for _, b := range []string{"main", "master"} {
		out, err := run(dir, "rev-parse", "--symbolic-full-name", b+"@{upstream}")
		if err != nil {
			continue
		}
		if ref := strings.TrimSpace(out); strings.HasPrefix(ref, "refs/remotes/") && refExists(dir, ref) {
			return ref
		}
	}
	return ""
}

func refExists(dir, ref string) bool {
	_, err := run(dir, "rev-parse", "--verify", "-q", ref+"^{commit}")
	return err == nil
}

// madeByThisFolder reports whether a HEAD reflog subject is one that CREATES a commit
// here: a commit, an amend, a merge commit, a cherry-pick, a revert, a rebased pick or
// an applied patch. A commit that was only pulled, fast-forwarded to or checked out is
// someone else's work.
func madeByThisFolder(subject string) bool {
	for _, p := range []string{"commit", "cherry-pick", "revert", "am"} {
		if subject == p || strings.HasPrefix(subject, p+":") || strings.HasPrefix(subject, p+" (") {
			return true
		}
	}
	for _, p := range []string{"rebase (pick)", "rebase (reword)", "rebase (squash)", "rebase (fixup)", "rebase (edit)", "rebase -i (pick)", "rebase -i (reword)", "rebase -i (squash)", "rebase -i (fixup)", "rebase -i (edit)"} {
		if strings.HasPrefix(subject, p) {
			return true
		}
	}
	return false
}

func madeShas(dir string) map[string]bool {
	made := map[string]bool{}
	out, err := run(dir, "reflog", "show", "HEAD", "--format=%H%x09%gs")
	if err != nil {
		return made
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, "\t", 2)
		if len(f) == 2 && madeByThisFolder(f[1]) {
			made[f[0]] = true
		}
	}
	return made
}

// ExcludeUpstream narrows a range to the session's own work. A commit already reachable
// from the repository's default branch on the remote (a local remote-tracking ref) is
// not the session's work when it was merely pulled or rebased onto, so the base moves up
// to the merge base of the head with that ref, unless that would skip a commit THIS
// folder made (its reflog shows it being created: a commit that later landed on the
// default branch by a push is still the session's, until it passed). A commit reachable
// only from a feature branch's remote ref is not excluded. The base never moves earlier,
// and a range that cannot be narrowed (no default ref, no common history, a base that is
// not an ancestor of the merge base) is returned as it was.
func ExcludeUpstream(dir string, r Range) (Range, error) {
	if r.Base == r.Head || r.Base == "" {
		return r, nil
	}
	ref := DefaultRemoteRef(dir)
	if ref == "" {
		return r, nil
	}
	c, found, err := mergeBaseWithHead(dir, ref, r.Head)
	if err != nil || !found || c == r.Base {
		return r, nil
	}
	if r.Base != EmptyTree {
		ok, err := IsAncestor(dir, r.Base, c)
		if err != nil || !ok {
			return r, nil
		}
	}
	span := c
	if r.Base != EmptyTree {
		span = r.Base + ".." + c
	}
	out, err := run(dir, "rev-list", span)
	if err != nil {
		return r, nil
	}
	made := madeShas(dir)
	for _, sha := range strings.Fields(out) {
		if !made[sha] {
			continue
		}
		po, err := run(dir, "rev-parse", "--verify", "-q", sha+"^")
		if err != nil {
			return r, nil // a root commit of the session's own: nothing before it to skip
		}
		parent := strings.TrimSpace(po)
		if ok, err := IsAncestor(dir, parent, c); err == nil && ok {
			c = parent
		}
	}
	if r.Base != EmptyTree {
		if ok, err := IsAncestor(dir, c, r.Base); err == nil && ok {
			return r, nil // never earlier than the base it already had
		}
	}
	r.Base = c
	return r, nil
}

// MergeBaseWithUpstream is the merge base of HEAD and the remote default branch (origin/HEAD,
// else origin/main): the newest commit already on it, so everything after is not. ok is false
// when there is no such branch or no shared history.
func MergeBaseWithUpstream(dir string) (sha string, ok bool, err error) {
	up := upstreamRef(dir)
	if up == "" {
		return "", false, nil
	}
	return mergeBaseWithHead(dir, up, "HEAD")
}
