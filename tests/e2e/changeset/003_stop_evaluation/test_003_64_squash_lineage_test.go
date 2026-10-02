package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Squash lineage. A pull request squash-merged on the remote puts a NEW commit on the default
// branch, and the local branch is then deleted (`gh pr merge --squash --delete-branch`): the
// session's own commits are held by nothing but the tracked range, and the squash commit is
// the remote's, not the session's: it is never blamed on the session.

// landUpstream is a pull request merge: a squash commit with the branch's tree appears on
// the remote's default branch (as `gh pr merge --squash` makes it, not by any commit of this
// folder), and the remote-tracking ref moves to it.
func landUpstream(id, branch string) harness.Turn {
	return Bash(id, "git update-ref refs/remotes/origin/main \"$(git commit-tree "+branch+"^{tree} -p refs/remotes/origin/main -m 'squash "+branch+"')\"")
}

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

// T003_64: a squash-merged and deleted branch. Everything the session committed is judged, so
// the violation is refused whole, and only the session's own commits are in what was judged.
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
}
