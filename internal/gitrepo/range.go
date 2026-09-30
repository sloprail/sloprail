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

// BaseOrigin says which candidate a Range's base came from.
type BaseOrigin string

const (
	// FromWatermark: the last head the rule passed, still reachable.
	FromWatermark BaseOrigin = "watermark"
	// FromFloor: the last commit that touched the rule's folder.
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
//  1. watermark — the last head the rule passed (when non-empty);
//  2. the last commit touching folder — the rule's definition, for a rule whose
//     folder is in this repository (folder is "" for one that is not, such as a
//     plugin's; a folder no commit has touched yet, an uncommitted rule, has no
//     such commit and falls through);
//  3. sessionStart — the HEAD recorded when the session began. Rules apply going
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
	if strings.TrimSpace(folder) != "" {
		floor, err := folderFloor(dir, folder)
		if err != nil {
			return Range{}, err
		}
		if floor != "" {
			// `git log` on HEAD makes it reachable by construction; it is asked
			// anyway, because "every run" is the rule and a cheap check is what
			// keeps it true if that construction ever changes.
			ok, err := Contains(dir, floor)
			if err != nil {
				return Range{}, fmt.Errorf("gitrepo: is %s an ancestor of HEAD: %w", floor, err)
			}
			if !ok {
				return Range{}, fmt.Errorf("gitrepo: floor %s for %q is not an ancestor of HEAD", floor, folder)
			}
			r.Base, r.Origin = floor, FromFloor
			return r, nil
		}
	}
	if sessionStart == "" {
		return Range{}, ErrNoSessionStart
	}
	ok, err := Contains(dir, sessionStart)
	if err != nil {
		return Range{}, fmt.Errorf("gitrepo: is %s an ancestor of HEAD: %w", sessionStart, err)
	}
	if !ok {
		return Range{}, fmt.Errorf("%w: %s", ErrSessionStartUnreachable, sessionStart)
	}
	r.Base, r.Origin = sessionStart, FromSessionStart
	return r, nil
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
