package gitrepo

import (
	"errors"
	"fmt"
	"strings"
)

// The range a file-guard judges: base..head.
//
// Every base here is a SHA. A branch name moves under amend, rebase and a
// branch switch, and a base picked by name (or per attempt) silently shrank
// a10n's diffs to the latest attempt while the failed checks never re-ran. A
// SHA that is no longer an ancestor of HEAD is detected on every call and
// dropped, so the next candidate is used instead.

// ErrNoCommits reports a repository with no commit yet: there is no head to
// judge. A sentinel because it is an ordinary state (the first session of a new
// project) that is NOT the same as an empty range, and a caller must not fold
// the two together.
var ErrNoCommits = errors.New("gitrepo: the repository has no commits yet")

// ErrNoSessionStart reports that the third floor was needed and the session's
// start HEAD was never recorded. Fail closed: with no watermark and no commit
// touching the rule, "where this session began" is the only remaining place a
// range can start, and guessing another would silently change what is judged.
var ErrNoSessionStart = errors.New("gitrepo: no watermark, no commit touching the rule's folder, and no session-start commit recorded")

// ErrSessionStartUnreachable reports that the recorded session-start commit is
// no longer an ancestor of HEAD (the tree left that history). Fail closed for the
// same reason: the last resort cannot be used, and nothing else is left.
var ErrSessionStartUnreachable = errors.New("gitrepo: the session-start commit is not an ancestor of HEAD")

// EmptyTree is git's empty tree, the base of a range that starts before the first
// commit. It is the same object in every repository, so it needs no lookup.
const EmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// BaseOrigin says which candidate a Range's base came from.
type BaseOrigin string

const (
	// FromWatermark: the last head the rule passed, still reachable.
	FromWatermark BaseOrigin = "watermark"
	// FromFloor: the PARENT of the last commit that touched the rule's .sloprail
	// root (the empty tree when that commit is a root), so the commit that adds or
	// changes a rule is itself judged by it.
	FromFloor BaseOrigin = "floor"
	// FromSessionStart: the HEAD recorded when the session began. The floor of
	// last resort, for a rule with no folder in this repository (a plugin's lives
	// in the plugin cache) or one not committed yet.
	FromSessionStart BaseOrigin = "session-start"
)

// Range is a computed span of history: what a file-guard judges.
//
// Base == Head is a real, computed, empty range — distinct from every error
// this package returns, which never produce a Range at all.
type Range struct {
	Base   string
	Head   string
	Origin BaseOrigin
	// DroppedWatermark is the watermark that was offered and found unreachable
	// (empty when none was offered or it was used). It is carried out so a caller
	// can say WHY the range widened after an amend or a rebase, instead of the
	// diff silently changing shape.
	DroppedWatermark string
}

// Empty reports whether the range holds no commits.
func (r Range) Empty() bool { return r.Base == r.Head }

// ResolveRange computes the range for one rule.
//
// base is the first reachable of, in order:
//
//  1. watermark — the last head the rule passed (when non-empty), at any
//     definition of the rule: work up to it was approved;
//  2. otherwise the EARLIER, in ancestry, of the two below (so nothing made in this
//     session is skipped, and history from before both stays grandfathered):
//     a. the PARENT of the last commit touching folder — the rule's definition, for
//     a rule whose folder is in this repository (folder is "" for one that is not,
//     such as a plugin's; a folder no commit has touched yet, an uncommitted rule,
//     has no such commit and falls through). The parent, not the commit: everything
//     else in the commit that adds or changes a rule is judged by the rule, so
//     touching the rule's folder is not a way to get work past it. A root commit
//     has no parent, and its base is the empty tree, so the whole of it is judged;
//     b. sessionStart — the HEAD recorded when the session began. Rules apply going
//     forward, and for a rule with no committed definition "forward" starts where
//     this session did.
//
// head is HEAD, as a SHA. folder is relative to dir, or repository-relative.
//
// Any failure of git is returned as an error and produces no Range: it is never
// read as "nothing changed". A candidate git does not have (gc'd after a rebase)
// counts as unreachable; one git could not be asked about is an error. When even
// the last floor is missing or unreachable the result is an error, never a guess.
func ResolveRange(dir, folder, watermark, sessionStart string) (Range, error) {
	head, err := headSHA(dir)
	if err != nil {
		return Range{}, err
	}
	r := Range{Head: head}
	if watermark != "" {
		ok, err := Contains(dir, watermark)
		if err != nil {
			return Range{}, fmt.Errorf("gitrepo: is %s an ancestor of HEAD: %w", watermark, err)
		}
		if ok {
			r.Base, r.Origin = watermark, FromWatermark
			return r, nil
		}
		r.DroppedWatermark = watermark
	}
	// No watermark: the EARLIER of the folder floor and the session start, so nothing
	// made in this session is skipped, while history from before both stays
	// grandfathered.
	var floor string
	if strings.TrimSpace(folder) != "" {
		last, err := folderFloor(dir, folder)
		if err != nil {
			return Range{}, err
		}
		if last != "" {
			// `git log` on HEAD makes it reachable by construction; it is asked
			// anyway, because "every run" is the rule and a cheap check is what
			// keeps it true if that construction ever changes.
			ok, err := Contains(dir, last)
			if err != nil {
				return Range{}, fmt.Errorf("gitrepo: is %s an ancestor of HEAD: %w", last, err)
			}
			if !ok {
				return Range{}, fmt.Errorf("gitrepo: floor %s for %q is not an ancestor of HEAD", last, folder)
			}
			if floor, err = parentOrEmptyTree(dir, last); err != nil {
				return Range{}, err
			}
		}
	}
	if floor == "" && sessionStart == "" {
		return Range{}, ErrNoSessionStart
	}
	start := ""
	if sessionStart != "" {
		ok, err := Contains(dir, sessionStart)
		if err != nil {
			return Range{}, fmt.Errorf("gitrepo: is %s an ancestor of HEAD: %w", sessionStart, err)
		}
		if ok {
			start = sessionStart
		} else {
			// The session start was rewritten (amend, rebase, reset). The floor alone
			// is NOT a safe stand-in: a later commit touching the rule's folder puts it
			// after in-session commits, which would then never be judged. The stand-in
			// is the merge base of HEAD with the remote's upstream/default branch: work
			// not yet on the remote is what is new. With no remote branch to anchor on
			// there is no way to tell which commits are new, so refuse.
			anchor, err := remoteAnchor(dir, head)
			if err != nil {
				return Range{}, err
			}
			if anchor == "" {
				return Range{}, fmt.Errorf("%w: %s (session start was rewritten; can't tell which commits are new)", ErrSessionStartUnreachable, sessionStart)
			}
			start = anchor
		}
	}
	switch {
	case floor == "":
		r.Base, r.Origin = start, FromSessionStart
	case start == "":
		r.Base, r.Origin = floor, FromFloor
	default:
		early, err := earlier(dir, floor, start)
		if err != nil {
			return Range{}, err
		}
		r.Base, r.Origin = early, FromFloor
		if early == start {
			r.Origin = FromSessionStart
		}
	}
	return r, nil
}

// earlier is whichever of two commits (or the empty tree) comes first in ancestry;
// for two that are not one another's ancestors, their merge base.
func earlier(dir, a, b string) (string, error) {
	if a == b {
		return a, nil
	}
	if a == EmptyTree || b == EmptyTree {
		return EmptyTree, nil
	}
	out, err := run(dir, "merge-base", a, b)
	if err != nil {
		return "", fmt.Errorf("gitrepo: merge base of %s and %s: %w", short(a), short(b), err)
	}
	base := strings.TrimSpace(out)
	if !isObjectName(base) {
		return "", fmt.Errorf("gitrepo: merge base resolved to %q, not an object name", base)
	}
	return base, nil
}

// headSHA is HEAD's full object name, or ErrNoCommits on an unborn HEAD.
func headSHA(dir string) (string, error) {
	// `--verify -q` exits 1, silently, when HEAD names nothing yet. Any other
	// failure is a fault and comes back as one.
	out, err := run(dir, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		if exitCode(err) == 1 {
			return "", ErrNoCommits
		}
		return "", err
	}
	sha := strings.TrimSpace(out)
	if !isObjectName(sha) {
		return "", fmt.Errorf("gitrepo: HEAD resolved to %q, not an object name", sha)
	}
	return sha, nil
}

// folderFloor is the last commit reachable from HEAD that touched folder, or ""
// when none did.
func folderFloor(dir, folder string) (string, error) {
	out, err := run(dir, "log", "-1", "--format=%H", "HEAD", "--", folder)
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(out)
	if sha != "" && !isObjectName(sha) {
		return "", fmt.Errorf("gitrepo: floor for %q resolved to %q, not an object name", folder, sha)
	}
	return sha, nil
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

// parentOrEmptyTree is the first parent of commit, or the empty tree for a root.
func parentOrEmptyTree(dir, commit string) (string, error) {
	// `--verify -q` exits 1, silently, when the commit has no parent.
	out, err := run(dir, "rev-parse", "--verify", "-q", commit+"^")
	if err != nil {
		if exitCode(err) == 1 {
			return EmptyTree, nil
		}
		return "", err
	}
	sha := strings.TrimSpace(out)
	if !isObjectName(sha) {
		return "", fmt.Errorf("gitrepo: parent of %s resolved to %q, not an object name", short(commit), sha)
	}
	return sha, nil
}

// remoteAnchor is the merge base of head with the first remote branch that
// exists, in order: HEAD's upstream, origin/HEAD, origin/main, origin/master. ""
// when there is none. Remote refs only: a local default branch that HEAD sits on
// would make the merge base HEAD itself, an empty range.
func remoteAnchor(dir, head string) (string, error) {
	var candidates []string
	if out, err := run(dir, "rev-parse", "--verify", "-q", "--symbolic-full-name", "@{upstream}"); err == nil {
		if name := strings.TrimSpace(out); name != "" {
			candidates = append(candidates, name)
		}
	} else if exitCode(err) != 1 && exitCode(err) != 128 {
		return "", err
	}
	candidates = append(candidates, "refs/remotes/origin/HEAD", "refs/remotes/origin/main", "refs/remotes/origin/master")
	for _, ref := range candidates {
		out, err := run(dir, "rev-parse", "--verify", "-q", ref+"^{commit}")
		if err != nil {
			if exitCode(err) == 1 || exitCode(err) == 128 {
				continue
			}
			return "", err
		}
		tip := strings.TrimSpace(out)
		if !isObjectName(tip) {
			return "", fmt.Errorf("gitrepo: %s resolved to %q, not an object name", ref, tip)
		}
		mb, err := run(dir, "merge-base", head, tip)
		if err != nil {
			if exitCode(err) == 1 { // unrelated histories
				continue
			}
			return "", fmt.Errorf("gitrepo: merge base of HEAD and %s: %w", ref, err)
		}
		base := strings.TrimSpace(mb)
		if !isObjectName(base) {
			return "", fmt.Errorf("gitrepo: merge base resolved to %q, not an object name", base)
		}
		return base, nil
	}
	return "", nil
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
