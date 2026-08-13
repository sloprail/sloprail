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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	// Empty on a detached HEAD that belongs to no operation — a plain checkout
	// of a bare commit. Two such states are NOT interchangeable: each names a
	// different commit, and comparing them by this field alone would call two
	// unrelated points the same place. Whoever compares positions has to reach
	// for the commit as well, which is why Sameness exists rather than a bare
	// equality on this field.
	//
	// During a rebase or a bisect it is the branch the operation is being run
	// ON, recovered from the operation's own record. HEAD is detached and walks
	// a different commit at every step, but the line of history the person is
	// working on has not changed — so the value holds still across the whole
	// operation, which is what keeps a rebasing session from re-baselining on
	// every cycle.
	Branch string

	// Operation says the branch above was recovered from an in-progress rebase
	// or bisect rather than read off an attached HEAD.
	//
	// The distinction matters to anyone holding a position still while the
	// operation runs. A rebase rewrites commits, so once it FINISHES the branch
	// reads the same as it always did while the history under it has been
	// replaced — and a caller that kept trusting the name would keep a position
	// on a commit the rebase discarded. This is what marks the difference
	// between "hold, this is still moving" and "settled, ask properly".
	Operation bool
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
	p, err := parseBranchHeaders(out)
	if err != nil || p.Commit == "" {
		return p, err
	}
	if p.Branch == "" {
		// Detached. Before accepting that as "no line of history", ask whether
		// an operation is in progress that knows better — a rebase and a bisect
		// both detach HEAD while the branch being worked on is unchanged.
		if branch := operationBranch(dir); branch != "" {
			p.Branch = branch
			p.Operation = true
		}
	}
	return p, nil
}

// operationBranch is the branch an in-progress operation is being run on, or ""
// when no operation is running.
//
// A rebase and a bisect both detach HEAD and then move it — a rebase stopped at
// `edit` sits on a different commit at every step. Read as a bare detached HEAD,
// each step looks like the tree arriving on a new line of history, and a caller
// re-taking its point on that reading would re-baseline several times over a
// single rebase. Both operations record the branch they started from, and that
// is the answer that stays still for as long as the operation runs.
//
// Absence is the ordinary answer, not a failure: most of the time no operation
// is running, and a detached HEAD that belongs to none really has no branch.
// Every read that fails is treated the same way, since a file that cannot be
// read tells us nothing about a branch, and the caller compares on the commit
// anyway when this is empty.
func operationBranch(dir string) string {
	// Ordered as git itself checks: rebase-merge covers interactive and merge
	// rebases, rebase-apply covers `git am` and the older rebase path.
	for _, rel := range []string{"rebase-merge/head-name", "rebase-apply/head-name"} {
		if ref := readGitFile(dir, rel); ref != "" {
			// Recorded fully qualified, as "refs/heads/main".
			return strings.TrimPrefix(ref, "refs/heads/")
		}
	}
	// A bisect records the branch it was started from, unqualified. It may also
	// hold a bare object name when the bisect began from a detached HEAD, in
	// which case there is no branch and the object name is not one.
	if start := readGitFile(dir, "BISECT_START"); start != "" && !isObjectName(start) {
		return start
	}
	return ""
}

// readGitFile reads one of git's own control files, or "" if it is not there.
//
// The path comes from `git rev-parse --git-path` rather than from joining
// ".git" onto dir. A linked worktree keeps these files somewhere else entirely,
// and a rebase running in one is exactly the case this is here to recognise, so
// guessing the location would fail precisely where it matters.
func readGitFile(dir, rel string) string {
	out, err := run(dir, "rev-parse", "--git-path", rel)
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(out)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		// Reported relative to the working tree's root rather than to dir.
		root, err := run(dir, "rev-parse", "--show-toplevel")
		if err != nil {
			return ""
		}
		path = filepath.Join(strings.TrimSpace(root), path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// objectName matches a full hexadecimal object name, the form git writes when
// it records a commit rather than a branch.
var objectName = regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`)

// isObjectName says whether a recorded value is a commit rather than a branch
// name. Both lengths are matched because a repository may be sha1 or sha256.
func isObjectName(s string) bool { return objectName.MatchString(s) }

// Contains reports whether commit is reachable from the tree's current HEAD.
//
// This is what tells a line of history the tree is still ON from one it has
// LEFT. A branch name cannot do it alone: `git checkout -b feature` and
// `git branch -m` both change the name while the commit and its whole history
// stay exactly where they were, and two unrelated detached checkouts share the
// same empty name while being nowhere near each other.
//
// An unreachable commit means the recorded point describes a history this tree
// no longer has. A reachable one means everything between it and HEAD is work
// that arrived along the way — which is the session's own, and must stay inside
// the difference rather than being pushed out of it.
//
// A commit git does not have is reported as not reachable rather than as an
// error: an object that is gone (a rebase dropped it, a reset discarded it) is
// a real state, and it is one where the recorded point is no longer a place
// this tree can measure from.
//
// A repository that cannot be read at all is a different thing and is returned
// as an error. "Not reachable" would be a claim about a history nothing here
// managed to look at, and a caller acting on it would move its point on the
// strength of a failure — the same silent wrong answer F4 was about, in the
// other direction. Callers reach Head first, which fails on a broken repository
// before this is ever asked, so this is the second lock rather than the first.
func Contains(dir, commit string) (bool, error) {
	if commit == "" {
		return false, nil
	}
	_, err := run(dir, "merge-base", "--is-ancestor", commit, "HEAD")
	if err == nil {
		return true, nil
	}
	// Exit 1 is the documented "not an ancestor", and the only clean negative.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	if errors.Is(err, ErrNotARepository) {
		return false, err
	}
	// Exit 128 covers both "no such object" and a repository git cannot read,
	// and the message is the only thing separating them. An object git does not
	// have is a real answer; anything else is a fault and is reported.
	if exitErr != nil && exitErr.ExitCode() == 128 && isUnknownObject(string(exitErr.Stderr)) {
		return false, nil
	}
	return false, err
}

// isUnknownObject recognises git declining because the object named is not in
// this repository — as opposed to declining because the repository itself is
// unreadable, which exits the same way.
// The spellings are git's own, checked against it rather than guessed: "not a
// valid commit name" is what `merge-base` says for an object it does not have.
func isUnknownObject(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "not a valid commit name") ||
		strings.Contains(lower, "not a valid object name") ||
		strings.Contains(lower, "no such ref") ||
		strings.Contains(lower, "bad object") ||
		strings.Contains(lower, "malformed object name")
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
		if isNotARepository(stderr) && !hasGitDir(dir) {
			return "", fmt.Errorf("%w: %s", ErrNotARepository, dir)
		}
		return "", fmt.Errorf("gitrepo: git %s in %s: %w: %s",
			strings.Join(args, " "), dir, err, stderr)
	}
	// git is not installed, or the directory does not exist. Neither is a
	// repository question, and both are reported as themselves.
	return "", fmt.Errorf("gitrepo: git %s in %s: %w", strings.Join(args, " "), dir, err)
}

// isNotARepository recognises the message git prints when it declines to find a
// repository.
//
// The message alone is not enough to conclude anything, and an earlier version
// of this claimed otherwise. git says "fatal: not a git repository" both when
// there is genuinely no repository and when there is one it cannot read — a
// corrupted HEAD produces exactly that sentence — and every failure of this
// command exits 128, so neither the status nor the wording separates the two.
// The claim that a wrong guess here "degrades to a reported error" was false in
// the direction that matters: it degraded to the absent-position answer, where
// the session silently stops measuring anything and says nothing about why.
//
// So this is only half the question, and hasGitDir asks the other half. Both
// spellings are checked because the second is what git says when the directory
// itself is missing.
func isNotARepository(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "not a git repository") ||
		strings.Contains(lower, "cannot change to")
}

// hasGitDir reports whether a repository appears to be present at dir or above
// it, judged from the filesystem rather than from what git said.
//
// This is what separates "there is no repository" from "there is one and it is
// broken". A .git that exists while git refuses to read it is a fault, and a
// fault has to be reported: treated as an absence it becomes an absent
// position, the recorded baseline is abandoned, and the session measures
// nothing while printing nothing.
//
// Walking upwards because a repository at any ancestor makes dir part of it,
// which is the same search git performs. A .git FILE rather than a directory is
// accepted too: that is what a linked worktree and a submodule both have.
//
// Erring towards "present" on an unreadable path. Reporting a fault about a
// directory that turns out to have no repository costs a line on stderr;
// missing a real fault costs the measurement, silently.
func hasGitDir(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	// GIT_CEILING_DIRECTORIES stops git's own upward search, so it has to stop
	// this one too, or the two would disagree about where a repository begins.
	ceilings := ceilingDirs()
	for {
		if _, err := os.Lstat(filepath.Join(abs, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(abs)
		if parent == abs || ceilings[parent] {
			return false
		}
		abs = parent
	}
}

// ceilingDirs is the set of directories git will not search above, as the
// environment names them.
func ceilingDirs() map[string]bool {
	set := map[string]bool{}
	for _, d := range filepath.SplitList(os.Getenv("GIT_CEILING_DIRECTORIES")) {
		if d != "" {
			set[d] = true
		}
	}
	return set
}
