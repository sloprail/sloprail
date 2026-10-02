package gitrepo

import (
	"errors"
	"fmt"
	"strconv"
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
// with the REMOTE default branch (origin's HEAD, else origin/main, origin/master). A local main
// is never taken for it: on a clone without a remote, or on main itself, it would make base ==
// head, an empty range that silently passes. ok is false when there is no remote default branch,
// so the caller must use a base it recorded or refuse; when the remote default branch exists but
// head shares no history with it, the base is git's empty tree (the branch truly has no base).
func DefaultBase(dir, head string) (sha string, ok bool) {
	if mb, found := RemoteDefaultBase(dir, head); found {
		return mb, true
	}
	if remoteDefaultRef(dir) != "" {
		return EmptyTree, true
	}
	return "", false
}

// remoteDefaultRef is the first remote default branch that exists, or "".
func remoteDefaultRef(dir string) string {
	for _, c := range remoteDefaultCandidates(dir) {
		if _, err := commitOf(dir, c, "--base"); err == nil {
			return c
		}
	}
	return ""
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

// CommittedHere reports whether this clone made sha on ref (a branch's full name, or HEAD): the
// ref's reflog records a commit, amend, merge, cherry-pick or revert that produced sha at or
// after since (unix seconds, 0 for any time). A branch only checked out, fetched or fast-forwarded
// carries no such entry, so it is not "committed on" by whoever stood on it.
func CommittedHere(dir, ref, sha string, since int64) bool {
	out, err := run(dir, "reflog", "show", "--format=%H %ct %gs", ref)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.SplitN(line, " ", 3)
		if len(fields) < 3 || fields[0] != sha {
			continue
		}
		at, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || at < since {
			continue
		}
		for _, kind := range []string{"commit", "cherry-pick", "revert"} {
			if strings.HasPrefix(fields[2], kind) {
				return true
			}
		}
	}
	return false
}

// CommitTime is the committer date of rev in unix seconds, 0 when it does not resolve.
func CommitTime(dir, rev string) int64 {
	out, err := run(dir, "log", "-1", "--format=%ct", rev)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	return n
}
