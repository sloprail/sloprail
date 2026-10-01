package gitrepo

import (
	"strconv"
	"strings"
	"time"
)

// Two scoping principles for a range a rule judges, applied the same way to every
// recorded tip (HEAD, a branch, a sub-agent's worktree, an ad-hoc folder):
//
//   - RULE AGE: a rule judges only commits made after it came into force for the
//     session (RaiseBaseToTime, RuleAbsentFromLine).
//   - WHAT STILL STANDS: a tip that has already landed upstream is judged only on the
//     paths whose content upstream still holds as the tip left it (StandsUpstream).

// RaiseBaseToTime moves a range's base up to the newest commit on head's first-parent
// line committed strictly BEFORE since, so only commits made at or after since remain in
// the range. The base never moves earlier; a range with no commit before since, or whose
// base is already later, is returned as it was. since is compared at second precision, the
// resolution of a commit date: a commit in the same second as since counts as after it,
// the stricter side.
func RaiseBaseToTime(dir string, r Range, since time.Time) (Range, error) {
	if since.IsZero() || r.Base == r.Head || r.Head == "" {
		return r, nil
	}
	out, err := run(dir, "log", "--first-parent", "--format=%H %ct", r.Head)
	if err != nil {
		return r, err
	}
	cut := since.Unix()
	for _, line := range strings.Split(out, "\n") {
		sha, ts, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || !isObjectName(sha) {
			continue
		}
		n, err := strconv.ParseInt(ts, 10, 64)
		if err != nil || n >= cut {
			continue
		}
		if sha == r.Base {
			return r, nil
		}
		if r.Base != EmptyTree {
			if ok, err := IsAncestor(dir, sha, r.Base); err != nil || ok {
				return r, err // the base is already at or after it
			}
		}
		r.Base = sha
		return r, nil
	}
	return r, nil
}

// RuleAbsentFromLine reports whether a rule's folder (repository-relative) was never part
// of the history ending at tip while HEAD has it committed: it is absent from the tip's
// tree and no commit of that history touched it, so that line of work predates the rule.
// A rule that was there and was deleted is NOT absent (deleting a rule is a change of it,
// judged as one), and a rule committed nowhere is left to the session-start floor.
func RuleAbsentFromLine(dir, tip, folder string) bool {
	if strings.TrimSpace(folder) == "" || tip == "" {
		return false
	}
	if out, err := run(dir, "ls-tree", "--name-only", tip, "--", folder); err != nil || strings.TrimSpace(out) != "" {
		return false
	}
	if out, err := run(dir, "log", "-1", "--format=%H", tip, "--", folder); err != nil || strings.TrimSpace(out) != "" {
		return false
	}
	out, err := run(dir, "ls-tree", "--name-only", "HEAD", "--", folder)
	return err == nil && strings.TrimSpace(out) != ""
}

// UpstreamRef is the remote branch work lands on (origin/HEAD's target, else
// origin/main), or "" when none is known.
func UpstreamRef(dir string) string { return upstreamRef(dir) }

// StandsUpstream reports whether path (and oldPath, for a rename) holds the same content
// at upstream as at tip, a path absent from both included: the tip's contribution to it
// still stands. A path upstream has changed since was superseded by later commits, which
// are judged where they were made. Anything uncertain is true, so a file is never
// dropped from judgement on a doubt.
func StandsUpstream(dir, tip, up, path, oldPath string) bool {
	if up == "" || tip == "" {
		return true
	}
	same := func(p string) bool {
		a, ok1 := blobAt(dir, up, p)
		b, ok2 := blobAt(dir, tip, p)
		return !ok1 || !ok2 || a == b
	}
	if !same(path) {
		return false
	}
	return oldPath == "" || same(oldPath)
}

func blobAt(dir, rev, path string) (string, bool) {
	out, err := run(dir, "rev-parse", "--verify", "-q", rev+":"+path)
	if err != nil {
		if exitCode(err) == 1 {
			return "", true // absent there
		}
		return "", false
	}
	return strings.TrimSpace(out), true
}
