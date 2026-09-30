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

// ErrNoFloor reports that no commit reachable from HEAD touches the rule's
// folder: the rule was never committed, so there is no point in history it
// began to apply from. Not an empty range either — the rule is simply not in
// effect yet, and the caller decides what that means.
var ErrNoFloor = errors.New("gitrepo: no commit reachable from HEAD touches the rule's folder")

// BaseOrigin says which candidate a Range's base came from.
type BaseOrigin string

const (
	// FromWatermark: the last head the rule passed, still reachable.
	FromWatermark BaseOrigin = "watermark"
	// FromFloor: the last commit that touched the rule's folder.
	FromFloor BaseOrigin = "floor"
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
// base is the first reachable of: the watermark (when non-empty), then the last
// commit touching folder — the rule's definition. head is HEAD, as a SHA.
// folder is relative to dir, or repository-relative from the root.
//
// Any failure of git is returned as an error and produces no Range: it is never
// read as "nothing changed". A watermark git does not have (gc'd after a rebase)
// counts as unreachable; a watermark git could not be asked about is an error.
func ResolveRange(dir, folder, watermark string) (Range, error) {
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
	floor, err := folderFloor(dir, folder)
	if err != nil {
		return Range{}, err
	}
	// The floor came from `git log` on HEAD, so it is reachable by construction;
	// it is asked anyway, because "every run" is the rule and a cheap check is
	// what keeps it true if that construction ever changes.
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

// folderFloor is the last commit reachable from HEAD that touched folder.
func folderFloor(dir, folder string) (string, error) {
	if strings.TrimSpace(folder) == "" {
		return "", fmt.Errorf("gitrepo: a rule folder is required to find a floor")
	}
	out, err := run(dir, "log", "-1", "--format=%H", "HEAD", "--", folder)
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(out)
	if sha == "" {
		return "", ErrNoFloor
	}
	if !isObjectName(sha) {
		return "", fmt.Errorf("gitrepo: floor for %q resolved to %q, not an object name", folder, sha)
	}
	return sha, nil
}
