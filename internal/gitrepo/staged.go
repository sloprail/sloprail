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
// An index with unmerged entries, an amend with no HEAD, and any git failure are errors:
// a change that could not be read is never an empty one.
func StagedRange(dir string, amend bool) (Range, error) {
	head, hasHead := commitIfAny(dir, "HEAD")
	base, parent := EmptyTree, ""
	switch {
	case amend && !hasHead:
		return Range{}, fmt.Errorf("gitrepo: --amend with no commit to amend")
	case amend:
		if p, ok := commitIfAny(dir, "HEAD^"); ok {
			base, parent = p, p
		}
	case hasHead:
		base, parent = head, head
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
	if parent != "" {
		args = append(args, "-p", parent)
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
