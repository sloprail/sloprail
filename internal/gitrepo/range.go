package gitrepo

import (
	"errors"
	"fmt"
	"strings"
)

// The range a file-guard judges: base..head, stated by the caller.
//
// Every bound here is a SHA. A branch name moves under amend, rebase and a branch
// switch, so the names a caller passes are resolved once, and what is recorded and
// judged is the commits they named then.

// ErrNoCommits reports a repository with no commit yet: there is no head to
// judge. A sentinel because it is an ordinary state (a new project) that is NOT the
// same as an empty range, and a caller must not fold the two together.
var ErrNoCommits = errors.New("gitrepo: the repository has no commits yet")

// EmptyTree is git's empty tree, the base of a range that starts before the first
// commit. It is the same object in every repository, so it needs no lookup.
const EmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Range is a computed span of history: what a file-guard judges.
//
// Base == Head is a real, computed, empty range — distinct from every error
// this package returns, which never produce a Range at all.
type Range struct {
	Base string
	Head string
}

// Empty reports whether the range holds no commits.
func (r Range) Empty() bool { return r.Base == r.Head }

// ResolveRange is the range merge-base(base, head)..head, with three-dot diff
// semantics: base may be a branch name or any revision, and a base that is behind
// (a stale remote-tracking branch) only widens the range, never narrows it. Either
// revision that does not name a commit is an error saying which; two histories that
// share nothing are an error too, never an empty range. The empty tree is the one base that
// is not a commit: it stands for a range that starts before the first commit.
func ResolveRange(dir, baseRev, headRev string) (Range, error) {
	head, err := commitOf(dir, headRev, "--head")
	if err != nil {
		return Range{}, err
	}
	if baseRev == EmptyTree {
		// A session that began before the first commit: every commit up to head is the range.
		return Range{Base: EmptyTree, Head: head}, nil
	}
	base, err := commitOf(dir, baseRev, "--base")
	if err != nil {
		return Range{}, err
	}
	out, err := run(dir, "merge-base", base, head)
	if err != nil {
		if exitCode(err) == 1 {
			return Range{}, fmt.Errorf("gitrepo: --base %q and --head %q share no history, so there is no range between them", baseRev, headRev)
		}
		return Range{}, fmt.Errorf("gitrepo: merge base of %q and %q: %w", baseRev, headRev, err)
	}
	mb := strings.TrimSpace(out)
	if !isObjectName(mb) {
		return Range{}, fmt.Errorf("gitrepo: merge base resolved to %q, not an object name", mb)
	}
	return Range{Base: mb, Head: head}, nil
}

// commitOf is rev's commit as a full object name, or an error naming the flag whose
// value did not resolve.
func commitOf(dir, rev, flag string) (string, error) {
	if strings.HasPrefix(rev, "-") || rev == "" {
		return "", fmt.Errorf("gitrepo: %s %q is not a revision", flag, rev)
	}
	out, err := run(dir, "rev-parse", "--verify", "-q", rev+"^{commit}")
	if err != nil {
		if exitCode(err) == 1 {
			return "", fmt.Errorf("gitrepo: %s %q does not resolve to a commit in this repository", flag, rev)
		}
		return "", err
	}
	sha := strings.TrimSpace(out)
	if !isObjectName(sha) {
		return "", fmt.Errorf("gitrepo: %s %q resolved to %q, not an object name", flag, rev, sha)
	}
	return sha, nil
}

// IsAncestor reports whether commit a is an ancestor of (or equal to) b. A commit
// git does not have is not an ancestor of anything; any other failure is an error.
func IsAncestor(dir, a, b string) (bool, error) {
	_, err := run(dir, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	if exitCode(err) == 1 {
		return false, nil
	}
	if _, verr := run(dir, "rev-parse", "--verify", "-q", a+"^{commit}"); verr != nil && exitCode(verr) == 1 {
		return false, nil
	}
	return false, err
}

// RootCommit is the repository's root commit — the identity of the repository
// itself, which survives worktrees, branches and clones. With several roots
// (unrelated histories merged) the oldest by commit date, then by name, is the
// answer, so every caller in the repository agrees on it.
func RootCommit(dir string) (string, error) {
	out, err := run(dir, "rev-list", "--max-parents=0", "--reverse", "--date-order", "HEAD")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if sha := strings.TrimSpace(line); sha != "" {
			if !isObjectName(sha) {
				return "", fmt.Errorf("gitrepo: root commit resolved to %q, not an object name", sha)
			}
			return sha, nil
		}
	}
	return "", ErrNoCommits
}

// HeadPushed reports whether any remote-tracking branch contains HEAD: whether
// HEAD (or a commit built on it) has been pushed, so that rewriting HEAD would
// diverge from what others have.
func HeadPushed(dir string) (bool, error) {
	out, err := run(dir, "for-each-ref", "--count=1", "--format=%(refname)", "--contains", "HEAD", "refs/remotes")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// DefaultBase is where work on head started, for a range nobody stated: the merge base of head
// with the repository's default branch — origin's (refs/remotes/origin/HEAD, else origin/main,
// origin/master), else a local main or master — and git's empty tree (everything is judged)
// when there is none or head shares no history with it. Never an error: a base that cannot be
// found is the widest range, not a skipped one.
func DefaultBase(dir, head string) string {
	candidates := []string{}
	if out, err := run(dir, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if ref := strings.TrimSpace(out); ref != "" {
			candidates = append(candidates, ref)
		}
	}
	candidates = append(candidates, "origin/main", "origin/master", "main", "master")
	for _, c := range candidates {
		if _, err := commitOf(dir, c, "--base"); err != nil {
			continue
		}
		out, err := run(dir, "merge-base", c, head)
		if err != nil {
			continue
		}
		if mb := strings.TrimSpace(out); isObjectName(mb) {
			return mb
		}
	}
	return EmptyTree
}

// IsDefaultBranch reports whether branch is the repository's default branch: the one origin's
// HEAD names, else main or master.
func IsDefaultBranch(dir, branch string) bool {
	if branch == "" {
		return false
	}
	if out, err := run(dir, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if ref := strings.TrimPrefix(strings.TrimSpace(out), "origin/"); ref != "" {
			return ref == branch
		}
	}
	return branch == "main" || branch == "master"
}
