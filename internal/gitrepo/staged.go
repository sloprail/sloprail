package gitrepo

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// StagedRange is the range a commit being made would add: from the commit it builds on
// (HEAD, or HEAD's parent when amend is set; git's empty tree where there is none) to a
// candidate commit holding exactly what the index holds.
//
// The candidate is a dangling commit object (`write-tree` and `commit-tree`): no ref, no
// index entry and no working-tree file is touched, and git collects the object itself. It
// exists so everything that reads a range of commits (a changeset, its snapshot) reads the
// commit about to be made the way it reads one already made.
//
// While a merge is in progress (MERGE_HEAD), the candidate is the merge commit: its parents are HEAD
// and the merged tips, so the merged branches' own commits are in the range, and a file the merge
// took unchanged from a side is theirs, not the merge's. For an amend of a merge, likewise.
//
// An index with unmerged entries, an amend with no HEAD, and any git failure are errors:
// a change that could not be read is never an empty one.
func StagedRange(dir string, amend bool) (Range, error) {
	head, hasHead := commitIfAny(dir, "HEAD")
	base := EmptyTree
	var parents []string
	switch {
	case amend && !hasHead:
		return Range{}, fmt.Errorf("gitrepo: --amend with no commit to amend")
	case amend:
		// An amended merge keeps all its parents: it is still the merge of its sides.
		out, err := run(dir, "rev-list", "--parents", "-n", "1", head)
		if err != nil {
			return Range{}, fmt.Errorf("gitrepo: the parents of HEAD could not be read: %w", err)
		}
		if f := strings.Fields(out); len(f) > 1 {
			for _, p := range f[1:] {
				if !isObjectName(p) {
					return Range{}, fmt.Errorf("gitrepo: rev-list answered %q, not an object name", p)
				}
			}
			base, parents = f[1], f[1:]
		}
	case hasHead:
		base, parents = head, []string{head}
		// A commit concluding a merge (`git commit --no-edit`, or the one `git merge` makes) has
		// the merged branches' tips as further parents: the commit being made is the merge.
		for _, m := range strings.Fields(readGitFile(dir, "MERGE_HEAD")) {
			if !isObjectName(m) {
				return Range{}, fmt.Errorf("gitrepo: MERGE_HEAD names %q, not a commit", m)
			}
			parents = append(parents, m)
		}
	}
	out, err := run(dir, "write-tree")
	if err != nil {
		return Range{}, fmt.Errorf("gitrepo: the index could not be read as a tree: %w", err)
	}
	tree := strings.TrimSpace(out)
	if !isObjectName(tree) {
		return Range{}, fmt.Errorf("gitrepo: write-tree answered %q, not an object name", tree)
	}
	args := []string{"commit-tree", tree, "-m", "sloprail: the staged change"}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=sloprail", "GIT_AUTHOR_EMAIL=sloprail@localhost",
		"GIT_COMMITTER_NAME=sloprail", "GIT_COMMITTER_EMAIL=sloprail@localhost")
	b, err := cmd.Output()
	if err != nil {
		return Range{}, fmt.Errorf("gitrepo: the staged change could not be made a candidate commit: %w", err)
	}
	cand := strings.TrimSpace(string(b))
	if !isObjectName(cand) {
		return Range{}, fmt.Errorf("gitrepo: commit-tree answered %q, not an object name", cand)
	}
	return Range{Base: base, Head: cand}, nil
}

// HasCommits reports whether HEAD names a commit (false in a repository with none yet).
func HasCommits(dir string) bool {
	_, ok := commitIfAny(dir, "HEAD")
	return ok
}

func commitIfAny(dir, rev string) (string, bool) {
	out, err := run(dir, "rev-parse", "--verify", "-q", rev+"^{commit}")
	if err != nil {
		return "", false
	}
	sha := strings.TrimSpace(out)
	return sha, isObjectName(sha)
}

// IsMerge reports whether the commit has more than one parent.
func IsMerge(dir, rev string) (bool, error) {
	out, err := run(dir, "rev-list", "--parents", "-n", "1", rev)
	if err != nil {
		return false, fmt.Errorf("gitrepo: the parents of %s could not be read: %w", short(rev), err)
	}
	return len(strings.Fields(out)) > 2, nil
}
