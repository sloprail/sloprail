// Package gitrepo asks git what state a working tree is in.
//
// It exists so that git is one package's resource rather than every caller's.
// A cycle needs the commit it measures from and the line of history that commit
// belongs to; both come from git, and reaching for `exec.Command("git", ...)`
// wherever they are wanted would spread the parsing, the error handling, and
// the not-a-repository case across the codebase.
//
// This is the only package that shells out to git. Callers get their own types
// back and never see a command line, an exit status, or a line of porcelain.
package gitrepo

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrNotARepository reports that the directory is not inside a git working
// tree.
//
// A sentinel because the caller has a real decision to make on it, and it is
// not the same decision as "git is broken". A project that is not a repository
// is an ordinary thing for a person to have — the engine records no baseline
// and carries on rather than refusing to start a session over it.
var ErrNotARepository = errors.New("gitrepo: not a git repository")

// Position is where a working tree sits in its history.
//
// The two travel together because neither answers the question alone. The
// commit is what a difference is measured from; the branch is what makes a
// switch to another line of history noticeable at all. A point recorded without
// its branch describes a history that may no longer be the tree's, with nothing
// to say so.
type Position struct {
	// Commit is the object name HEAD resolves to, in full.
	Commit string

	// Branch is the line of history HEAD is on.
	//
	// Empty on a detached HEAD, which is a real state rather than a failure —
	// a rebase, a bisect, or a checkout of a bare commit all produce one. An
	// empty branch compares equal to the next empty branch, so a session that
	// starts detached and stays detached is not re-baselined on every cycle;
	// moving onto or off a branch changes the value and is noticed.
	Branch string
}

// Head reports where the working tree at dir currently sits.
//
// Both facts come from one process rather than two. `git status` already
// carries the branch and the commit together and reports them as data rather
// than as prose, so asking twice would cost a second fork and open a window in
// which the tree could move between the two answers — producing a commit from
// one line of history recorded against the name of another, which is precisely
// the state this exists to detect.
func Head(dir string) (Position, error) {
	out, err := run(dir, "status", "--porcelain=v2", "--branch", "--untracked-files=no")
	if err != nil {
		return Position{}, err
	}
	return parseBranchHeaders(out)
}

// noCommitYet is what porcelain v2 reports for HEAD in a repository that has
// none. A repository initialised but never committed is an ordinary state — the
// very first session in a new project is in it — and it is not a position
// anything can be measured from.
const noCommitYet = "(initial)"

// detachedHead is what porcelain v2 reports for the branch when HEAD names no
// branch.
const detachedHead = "(detached)"

// parseBranchHeaders pulls the position out of porcelain v2's header lines.
//
// The headers are the reason for --porcelain=v2 over the friendlier output:
// `# branch.oid <sha>` and `# branch.head <name>` are documented, stable, and
// not translated, whereas the human-readable "On branch main" is all three of
// the opposite.
func parseBranchHeaders(out string) (Position, error) {
	var p Position
	for _, line := range strings.Split(out, "\n") {
		field, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		switch field {
		case "#":
			// A header line: "# branch.oid <value>". The switch above matched
			// the "#" and left the rest to be split again.
			name, v, ok := strings.Cut(value, " ")
			if !ok {
				continue
			}
			switch name {
			case "branch.oid":
				if v != noCommitYet {
					p.Commit = v
				}
			case "branch.head":
				if v != detachedHead {
					p.Branch = v
				}
			}
		}
	}
	if p.Commit == "" {
		// A repository with no commit yet. Reported as an absent position
		// rather than an error: there is nothing wrong, there is simply nothing
		// to measure from until the first commit exists.
		return Position{}, nil
	}
	return p, nil
}

// run executes a git command in dir and returns its standard output.
//
// A directory that is not a repository is turned into ErrNotARepository, so
// every caller recognises it the same way rather than each matching on git's
// own wording.
func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		return string(out), nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		stderr := strings.TrimSpace(string(exitErr.Stderr))
		if isNotARepository(stderr) {
			return "", fmt.Errorf("%w: %s", ErrNotARepository, dir)
		}
		return "", fmt.Errorf("gitrepo: git %s in %s: %w: %s",
			strings.Join(args, " "), dir, err, stderr)
	}
	// git is not installed, or the directory does not exist. Neither is a
	// repository question, and both are reported as themselves.
	return "", fmt.Errorf("gitrepo: git %s in %s: %w", strings.Join(args, " "), dir, err)
}

// isNotARepository recognises git declining because there is no repository
// here.
//
// Matched on git's message because git offers nothing else: every failure of
// this command exits 128, so the status cannot tell this apart from a genuine
// fault. Both spellings are checked — the second is what git says when the
// directory itself is missing — and a message that changes underneath this
// degrades to a reported error rather than to a wrong answer, which is the safe
// direction for a guess to be wrong in.
func isNotARepository(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "not a git repository") ||
		strings.Contains(lower, "cannot change to")
}
