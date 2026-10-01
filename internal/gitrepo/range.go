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
//  2. otherwise, for a rule that did NOT exist at session start (its folder is absent
//     from the session-start commit's tree: added during the session), the PARENT of
//     the commit that FIRST added the rule's own folder since the session began (a root
//     commit's parent is the empty tree), never later than the floor (the parent of the
//     last commit touching folder). The rule applies from that commit, and earlier
//     history is grandfathered. The parent, not the commit, so the commit that adds the
//     rule is itself judged by it; and not the last touch of the .sloprail root, which
//     would let a later touch move the base past violations committed after the rule
//     was added. For a rule that existed at
//     session start (also when the start is unknown or unborn, which fails closed to
//     "existed"), sessionStart: the HEAD recorded when the session began, never later, and
//     earlier only by refusedBases: the bases of ranges an EARLIER session of the same
//     worktree was refused for and never fixed (the caller reads them from the sibling
//     sessions' check stores), so a refusal does not vanish when a second session
//     starts. History that passed or was never checked stays grandfathered. The floor
//     does not matter for it: a rule last changed long before
//     the session must not re-judge every commit merged since, and touching .sloprail
//     mid-session cannot move the base past the session's own work. With no session
//     start recorded the floor is used, and with neither the result is ErrNoSessionStart.
//     (folder is "" for a rule that is not in this repository, such as a plugin's; it has
//     no floor and uses the session start.)
//
// Every anchor is tested with `git merge-base --is-ancestor <anchor> HEAD` on every
// run, and one that an amend, a rebase or a reset left is RE-ANCHORED at its merge base
// with HEAD (the point where the rewritten line and HEAD's still agree), not skipped:
//
//   - an unreachable watermark becomes merge-base(watermark, HEAD): what was approved
//     and survives stays approved, what was rewritten is judged again;
//   - an unreachable sessionStart becomes merge-base(sessionStart, HEAD), and the
//     empty tree (the whole history, root commit included) when git no longer has the commit (or shares no history
//     with it): everything made since the session began is still inside the range. The
//     folder floor alone would not do, because a later commit touching the rule's
//     folder puts it AFTER in-session commits, which would then never be judged.
//
// head is HEAD, as a SHA. folder is relative to dir, or repository-relative. rule is
// the rule's OWN folder (`.sloprail/file-guard/<name>`, same form), whose presence in
// the session-start tree decides whether the rule is new; "" falls back to folder.
//
// Any failure of git is returned as an error and produces no Range: it is never
// read as "nothing changed". A candidate git does not have (gc'd after a rebase)
// counts as unreachable; one git could not be asked about is an error. With no watermark,
// no committed definition and no session start recorded the result is
// ErrNoSessionStart, never a guess.
func ResolveRange(dir, folder, rule, watermark, sessionStart string, refusedBases ...string) (Range, error) {
	head, err := headSHA(dir)
	if err != nil {
		return Range{}, err
	}
	return ResolveRangeAt(dir, head, folder, rule, watermark, sessionStart, refusedBases...)
}

// ResolveRangeAt is ResolveRange for the history ending at head (a commit SHA) rather
// than HEAD: every ancestry question is asked of head. It is how a branch the working
// tree has left, or a detached-HEAD commit, is judged by the same logic as HEAD.
func ResolveRangeAt(dir, head, folder, rule, watermark, sessionStart string, refusedBases ...string) (Range, error) {
	r, err := resolveRangeAt(dir, head, folder, rule, watermark, sessionStart, refusedBases...)
	if err != nil {
		return Range{}, err
	}
	return ExcludeUpstream(dir, r)
}

func resolveRangeAt(dir, head, folder, rule, watermark, sessionStart string, refusedBases ...string) (Range, error) {
	r := Range{Head: head}
	if watermark != "" {
		ok, err := IsAncestor(dir, watermark, head)
		if err != nil {
			return Range{}, fmt.Errorf("gitrepo: is %s an ancestor of HEAD: %w", watermark, err)
		}
		if ok {
			r.Base, r.Origin = watermark, FromWatermark
			return r, nil
		}
		r.DroppedWatermark = watermark
		mb, found, err := mergeBaseWithHead(dir, watermark, head)
		if err != nil {
			return Range{}, err
		}
		if found {
			r.Base, r.Origin = mb, FromWatermark
			return r, nil
		}
	}
	// No watermark: a rule added during the session judges from its floor; a rule
	// that existed at session start judges from the session start.
	var floor string
	if strings.TrimSpace(folder) != "" {
		last, err := folderFloor(dir, head, folder)
		if err != nil {
			return Range{}, err
		}
		if last != "" {
			// `git log` on HEAD makes it reachable by construction; it is asked
			// anyway, because "every run" is the rule and a cheap check is what
			// keeps it true if that construction ever changes.
			ok, err := IsAncestor(dir, last, head)
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
	if floor != "" && !existedAt(dir, sessionStart, firstNonEmpty(rule, folder)) {
		// A rule that did not exist when the session began applies from the commit
		// that FIRST added its own folder since the session began: what came before
		// it is grandfathered, so adding a rule mid-session does not judge the whole
		// session. Not the last commit touching the .sloprail root (the floor): a later
		// touch of the root would move the base past violations committed after the rule
		// was added. Never later than the floor.
		base := floor
		added, err := firstAddCommit(dir, head, sessionStart, firstNonEmpty(rule, folder))
		if err != nil {
			return Range{}, err
		}
		if added != "" {
			parent, err := parentOrEmptyTree(dir, added)
			if err != nil {
				return Range{}, err
			}
			if base, err = earlier(dir, parent, floor); err != nil {
				return Range{}, err
			}
		}
		r.Base, r.Origin = base, FromFloor
		return r, nil
	}
	if floor == "" && sessionStart == "" {
		return Range{}, ErrNoSessionStart
	}
	start := ""
	if sessionStart != "" {
		ok := sessionStart == EmptyTree // a session that began before the first commit
		if !ok {
			var err error
			if ok, err = IsAncestor(dir, sessionStart, head); err != nil {
				return Range{}, fmt.Errorf("gitrepo: is %s an ancestor of HEAD: %w", sessionStart, err)
			}
		}
		if ok {
			start = sessionStart
		} else {
			s, err := reanchorSessionStart(dir, sessionStart, head)
			if err != nil {
				return Range{}, err
			}
			start = s
		}
	}
	if start != "" {
		r.Base, r.Origin = start, FromSessionStart
		// Extended backwards over every range an earlier session of this worktree was
		// refused for and never fixed: nothing it refused may fall out of sight.
		for _, rb := range refusedBases {
			if rb != "" && rb != EmptyTree {
				ok, err := IsAncestor(dir, rb, head)
				if err != nil {
					return Range{}, fmt.Errorf("gitrepo: is %s an ancestor of HEAD: %w", rb, err)
				}
				if !ok {
					if rb, err = reanchorSessionStart(dir, rb, head); err != nil {
						return Range{}, err
					}
				}
			}
			if rb == "" {
				continue
			}
			early, err := earlier(dir, r.Base, rb)
			if err != nil {
				return Range{}, err
			}
			r.Base = early
		}
	} else {
		r.Base, r.Origin = floor, FromFloor
	}
	return r, nil
}

// firstAddCommit is the OLDEST commit in sessionStart..HEAD that added a file under
// folder, or "" when there is none.
func firstAddCommit(dir, head, sessionStart, folder string) (string, error) {
	out, err := run(dir, "log", "--reverse", "--diff-filter=A", "--format=%H", sessionStart+".."+head, "--", folder)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if sha := strings.TrimSpace(line); sha != "" {
			if !isObjectName(sha) {
				return "", fmt.Errorf("gitrepo: add commit for %q resolved to %q, not an object name", folder, sha)
			}
			return sha, nil
		}
	}
	return "", nil
}

// firstNonEmpty is a unless it is blank, else b.
func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// existedAt reports whether the rule's folder is in the tree of the session-start
// commit. Fail closed: an unknown or unborn start (empty, or the empty tree), or a
// commit git cannot be asked about, counts as "existed", which keeps the stricter
// earlier-of range.
func existedAt(dir, sessionStart, folder string) bool {
	if sessionStart == "" || sessionStart == EmptyTree {
		return true
	}
	out, err := run(dir, "ls-tree", "--name-only", sessionStart, "--", folder)
	if err != nil {
		return true
	}
	return strings.TrimSpace(out) != ""
}

// IsAncestor reports whether a is an ancestor of b (or b itself). A commit git does
// not have is "not an ancestor"; any other git failure is an error.
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
func folderFloor(dir, head, folder string) (string, error) {
	out, err := run(dir, "log", "-1", "--format=%H", head, "--", folder)
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

// reanchorSessionStart is where a session start the tree left (an amend, a rebase, a
// reset) is measured from instead: its merge base with HEAD, or the empty tree when git
// has no such commit or it shares no history with HEAD (an amended root commit is a
// different root). The empty tree, not the root commit: the root's own content is judged
// too, as parentOrEmptyTree does for a rule added in the root commit. Never later than
// the work the session did.
func reanchorSessionStart(dir, sessionStart, head string) (string, error) {
	mb, found, err := mergeBaseWithHead(dir, sessionStart, head)
	if err != nil {
		return "", err
	}
	if found {
		return mb, nil
	}
	return EmptyTree, nil
}

// mergeBaseWithHead is the merge base of commit and head. found is false when git has no
// such commit (gc'd after a rewrite) or the two share no history: ordinary outcomes of a
// rewrite, not faults. Anything else git says is an error.
func mergeBaseWithHead(dir, commit, head string) (base string, found bool, err error) {
	if _, err := run(dir, "rev-parse", "--verify", "-q", commit+"^{commit}"); err != nil {
		if exitCode(err) == 1 {
			return "", false, nil
		}
		return "", false, err
	}
	out, err := run(dir, "merge-base", commit, head)
	if err != nil {
		if exitCode(err) == 1 { // no common ancestor
			return "", false, nil
		}
		return "", false, fmt.Errorf("gitrepo: merge base of %s and HEAD: %w", short(commit), err)
	}
	base = strings.TrimSpace(out)
	if !isObjectName(base) {
		return "", false, fmt.Errorf("gitrepo: merge base resolved to %q, not an object name", base)
	}
	return base, true, nil
}

// ReanchorWatermark is where a watermark the tree left (an amend, a rebase, a reset) is
// measured from instead: its merge base with HEAD. found is false when git no longer has
// the commit or it shares no history with HEAD, and the caller falls back to something
// else it has.
func ReanchorWatermark(dir, watermark string) (base string, found bool, err error) {
	head, err := headSHA(dir)
	if err != nil {
		return "", false, err
	}
	return mergeBaseWithHead(dir, watermark, head)
}

// ReanchorWatermarkAt is ReanchorWatermark for the history ending at head.
func ReanchorWatermarkAt(dir, head, watermark string) (base string, found bool, err error) {
	return mergeBaseWithHead(dir, watermark, head)
}
