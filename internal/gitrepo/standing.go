package gitrepo

import (
	"fmt"
	"strconv"
	"strings"
)

// RaiseBaseToRuleFloor is the RULE AGE scoping of an explicit range: a rule judges only the
// work made after it came into force. The rule's floor is the PARENT of the last commit
// reachable from r.Head that touched the rule's folder (repository-relative), so a rule
// added mid-branch applies from its add commit and what came before it is grandfathered.
// The effective base is the later of the range's base and that floor; a range already
// starting at or after the floor, a rule that already stood at the base, a rule committed nowhere on head's history, or a folder
// outside the repository is returned as it was.
func RaiseBaseToRuleFloor(dir string, r Range, folder string) (Range, error) {
	if strings.TrimSpace(folder) == "" || r.Head == "" || r.Base == r.Head {
		return r, nil
	}
	out, err := run(dir, "log", "-1", "--format=%H", r.Head, "--", folder)
	if err != nil {
		return r, err
	}
	last := strings.TrimSpace(out)
	if last == "" {
		return raiseByRuleDate(dir, r, folder)
	}
	if !isObjectName(last) {
		return r, fmt.Errorf("gitrepo: floor for %q resolved to %q, not an object name", folder, last)
	}
	if r.Base != EmptyTree {
		// A rule that already stood at the base keeps the strict range: editing, or deleting and
		// re-adding, it mid-range is no way to skip judging the earlier work.
		if _, err := run(dir, "cat-file", "-e", r.Base+":"+folder); err == nil {
			return r, nil
		}
	}
	floor, err := parentOrEmptyTree(dir, last)
	if err != nil {
		return r, err
	}
	if floor == EmptyTree || floor == r.Base {
		return r, nil
	}
	if r.Base != EmptyTree {
		if later, err := IsAncestor(dir, floor, r.Base); err != nil || later {
			return r, err // the base is already at or after the floor
		}
	}
	r.Base = floor
	return r, nil
}

// parentOrEmptyTree is commit's first parent, or the empty tree for a root commit.
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
		return "", fmt.Errorf("gitrepo: parent of %s resolved to %q, not an object name", commit, sha)
	}
	return sha, nil
}

// raiseByRuleDate is the floor of a rule that head's own history does not carry (an older branch,
// cut before the rule arrived): the rule is in force from the commit that last changed it in the
// checkout (HEAD) on, so the commits head made BEFORE that commit's date are not its debt. The
// base becomes the newest commit of the range older than that date; a range with none is returned
// as it was.
func raiseByRuleDate(dir string, r Range, folder string) (Range, error) {
	out, err := run(dir, "log", "-1", "--format=%ct", "HEAD", "--", folder)
	if err != nil {
		return r, nil // no checkout to ask: the rule has no floor
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return r, nil
	}
	if r.Base != EmptyTree {
		if _, err := run(dir, "cat-file", "-e", r.Base+":"+folder); err == nil {
			return r, nil
		}
	}
	args := []string{"rev-list", "-1", fmt.Sprintf("--before=%d", ts-1), r.Head}
	if r.Base != EmptyTree {
		args = append(args, "^"+r.Base)
	}
	out, err = run(dir, args...)
	if err != nil {
		return r, err
	}
	if sha := strings.TrimSpace(out); sha != "" {
		r.Base = sha
	}
	return r, nil
}
