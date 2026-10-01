package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Squash lineage. A pull request squash-merged on the remote puts a NEW commit on the default
// branch, and the local branch is then deleted (`gh pr merge --squash --delete-branch`): the
// session's own commits are held by nothing but the engine, and the squash commit is the
// remote's, not the session's. The owed tip is judged on the files whose content still
// stands on main (#148); the squash commit is never blamed on the session (#143).

func squashedAndDeleted(branch, main string) []harness.Turn {
	return []harness.Turn{
		Bash("q1", "git switch -q -c "+branch),
		harness.CommitFile("q2", "docs/a.md", "FORBIDDEN words", "add a"),
		harness.CommitFile("q3", "docs/b.md", "FORBIDDEN words", "add b"),
		Bash("q4", "git switch -q "+main),
		landUpstream("q5", branch),
		Bash("q6", "git branch -q -D "+branch),
	}
}

// T003_64: a clean squash. Everything the session committed still stands on main, so the
// violation is refused whole, and only the session's own commits are in what was judged.
// Rewritten upstream afterwards, every file is superseded: the tip is settled.
func TestT003_64_ASquashMergedAndDeletedBranchIsJudgedAndTheSquashCommitIsNot(t *testing.T) {
	e, proj, led := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	e.Git(proj, "update-ref", "refs/remotes/origin/main", "HEAD")
	const sess = "s-003-64"

	e.Run(proj, sess, "merge it and delete the branch", Turns("done", squashedAndDeleted("feat", main)...))
	got := stopRefusals(e, proj, sess)
	if !strings.Contains(got, refusalText) || !strings.Contains(got, "docs/a.md") || !strings.Contains(got, "docs/b.md") {
		t.Fatalf("a squash-merged and deleted branch with two violations was not refused for both files:\n%s", got)
	}
	for _, run := range ledger(t, led) {
		for _, c := range run.Commits {
			if strings.HasPrefix(c, "squash") {
				t.Fatalf("the squash commit, the remote's, was judged as the session's work: %v", run.Commits)
			}
		}
	}
	blocks := stopBlocks(e, proj, sess)

	e.Run(proj, sess, "main takes the fixes", Turns("done",
		rewriteUpstream("r1", "docs/a.md", "clean words"),
		rewriteUpstream("r2", "docs/b.md", "clean words"),
	))
	if n := stopBlocks(e, proj, sess); n != blocks {
		t.Fatalf("a tip every file of which main has since rewritten was still refused:\n%s", newBlocks(e, proj, sess, blocks))
	}
}

// T003_64: a squash that main later edited. The file main rewrote is superseded and not
// judged; the one main still holds as the session left it is, and refused until it is fixed
// there too.
func TestT003_64_ASquashMergedBranchMainLaterEditedIsJudgedOnWhatStands(t *testing.T) {
	e, proj, led := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	e.Git(proj, "update-ref", "refs/remotes/origin/main", "HEAD")
	const sess = "s-003-64b"

	e.Run(proj, sess, "merge it, delete the branch, main edits a", Turns("done",
		append(squashedAndDeleted("feat", main), rewriteUpstream("r1", "docs/a.md", "clean words"))...))
	got := stopRefusals(e, proj, sess)
	if !strings.Contains(got, refusalText) || !strings.Contains(got, "docs/b.md") {
		t.Fatalf("the file main still holds as the session left it was not refused:\n%s", got)
	}
	if strings.Contains(got, "docs/a.md") {
		t.Fatalf("a file main has since rewritten was judged as the session's debt:\n%s", got)
	}
	for _, run := range ledger(t, led) {
		if strings.Contains(strings.Join(paths(run.Files), " "), "docs/a.md") {
			t.Fatalf("a superseded file was handed to the rule: %v", paths(run.Files))
		}
	}
	blocks := stopBlocks(e, proj, sess)

	e.Run(proj, sess, "main takes the fix of b", Turns("done", rewriteUpstream("r2", "docs/b.md", "clean words")))
	if n := stopBlocks(e, proj, sess); n != blocks {
		t.Fatalf("the last file was rewritten on main and the tip was still refused:\n%s", newBlocks(e, proj, sess, blocks))
	}
}
