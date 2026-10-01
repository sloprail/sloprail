package gitrepo

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Tip is the end of a line of history a working tree made commits on: a commit and the
// ref it is recorded under. Ref is a full ref name ("refs/heads/feat-a"), or
// "detached/<sha12>" for commits that were made on a detached HEAD and left.
type Tip struct {
	Sha string
	Ref string
}

// DetachedRef is the name a detached-HEAD tip is recorded under.
func DetachedRef(sha string) string { return "detached/" + short(sha) }

// RefTip is the commit a full ref name points at now, or "" when the ref does not exist.
func RefTip(dir, ref string) (string, error) {
	out, err := run(dir, "rev-parse", "--verify", "-q", ref+"^{commit}")
	if err != nil {
		if exitCode(err) == 1 {
			return "", nil
		}
		return "", err
	}
	sha := strings.TrimSpace(out)
	if !isObjectName(sha) {
		return "", fmt.Errorf("gitrepo: %s resolved to %q, not an object name", ref, sha)
	}
	return sha, nil
}

// Reachable reports whether any branch, remote-tracking branch or tag contains commit.
func Reachable(dir, commit string) (bool, error) {
	out, err := run(dir, "for-each-ref", "--count=1", "--format=%(refname)", "--contains", commit,
		"refs/heads", "refs/remotes", "refs/tags")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// BranchNameFor names a ref for a commit made on a branch: the local branch whose tip it
// is, else the first local (then remote-tracking) branch that contains it, "" when none.
func BranchNameFor(dir, commit string) (string, error) {
	for _, prefix := range []string{"refs/heads", "refs/remotes"} {
		out, err := run(dir, "for-each-ref", "--format=%(objectname) %(refname)", "--contains", commit, prefix)
		if err != nil {
			return "", err
		}
		var first string
		for _, line := range strings.Split(out, "\n") {
			sha, ref, ok := strings.Cut(strings.TrimSpace(line), " ")
			if !ok || strings.HasSuffix(ref, "/HEAD") {
				continue
			}
			if sha == commit {
				return ref, nil
			}
			if first == "" || ref < first {
				first = ref
			}
		}
		if first != "" {
			return first, nil
		}
	}
	return "", nil
}

// Maximal drops every commit that is an ancestor of another in the list (and the
// duplicates), keeping the list's order: a commit that a later tip contains is judged
// with that tip, so nothing is judged twice.
func Maximal(dir string, shas []string) ([]string, error) {
	var uniq []string
	seen := map[string]bool{}
	for _, s := range shas {
		if s != "" && !seen[s] {
			seen[s] = true
			uniq = append(uniq, s)
		}
	}
	var out []string
	for i, a := range uniq {
		covered := false
		for j, b := range uniq {
			if i == j {
				continue
			}
			ok, err := IsAncestor(dir, a, b)
			if err != nil {
				return nil, err
			}
			if ok {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, a)
		}
	}
	return out, nil
}

var hexName = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// madeCommit is the reflog subject of an entry that put a NEW commit at HEAD (as opposed
// to a checkout, a reset or a clone, which only move HEAD to something that exists).
func madeCommit(subject string) bool {
	for _, p := range []string{"commit", "cherry-pick", "revert", "merge", "pull", "rebase", "am"} {
		if subject == p || strings.HasPrefix(subject, p+":") || strings.HasPrefix(subject, p+" (") {
			return true
		}
	}
	return false
}

// ReflogTips reads the HEAD reflog of the working tree at dir (a worktree has its own)
// for the commits made since the given time, on any branch or on a detached HEAD, and
// returns each line of history's tip: the commits that are still reachable from a ref
// (named by it), and the ones left behind on a detached HEAD (named detached/<sha>).
// Commits an amend, a reset or a rebase left unreachable are not tips. Commits HEAD
// itself contains are left out; HEAD is judged as HEAD.
func ReflogTips(dir string, since time.Time) ([]Tip, error) {
	out, err := run(dir, "reflog", "show", "HEAD", "--date=unix", "--format=%H%x09%gd%x09%gs")
	if err != nil {
		if strings.Contains(err.Error(), "unknown revision") || strings.Contains(err.Error(), "ambiguous argument") {
			return nil, nil // unborn
		}
		return nil, err
	}
	type entry struct {
		sha, subject string
		at           int64
	}
	var entries []entry // newest first
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) != 3 || !isObjectName(f[0]) {
			continue
		}
		open, cl := strings.LastIndex(f[1], "{"), strings.LastIndex(f[1], "}")
		if open < 0 || cl < open {
			continue
		}
		at, err := strconv.ParseInt(f[1][open+1:cl], 10, 64)
		if err != nil {
			continue
		}
		entries = append(entries, entry{sha: f[0], subject: f[2], at: at})
	}
	floor := since.Unix()
	made := map[string]bool{}
	detached := map[string]bool{}
	var order []string
	add := func(m map[string]bool, sha string) {
		if !m[sha] && !made[sha] && !detached[sha] {
			order = append(order, sha)
		}
		m[sha] = true
	}
	for i, e := range entries {
		if e.at < floor {
			break
		}
		switch {
		case madeCommit(e.subject):
			add(made, e.sha)
		case strings.HasPrefix(e.subject, "checkout: moving from "):
			from, _, _ := strings.Cut(strings.TrimPrefix(e.subject, "checkout: moving from "), " to ")
			if hexName.MatchString(from) && i+1 < len(entries) {
				add(detached, entries[i+1].sha) // HEAD was detached here when it moved away
			}
		}
	}
	var tips []Tip
	for _, sha := range order {
		inHead, err := IsAncestor(dir, sha, "HEAD")
		if err != nil {
			return nil, err
		}
		if inHead {
			continue
		}
		name, err := BranchNameFor(dir, sha)
		if err != nil {
			return nil, err
		}
		switch {
		case name != "":
			tips = append(tips, Tip{Sha: sha, Ref: name})
		case detached[sha]:
			tips = append(tips, Tip{Sha: sha, Ref: DetachedRef(sha)})
		}
	}
	return tips, nil
}

// RefCreation is the commit a branch was created at: the oldest entry of the ref own
// reflog ("branch: Created from ..."). Empty when the ref has no reflog.
func RefCreation(dir, ref string) (string, error) {
	out, err := run(dir, "reflog", "show", "--format=%H", ref)
	if err != nil {
		return "", nil
	}
	lines := strings.Fields(out)
	if len(lines) == 0 || !isObjectName(lines[len(lines)-1]) {
		return "", nil
	}
	return lines[len(lines)-1], nil
}
