package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// squashInto is the agent squash-merging a branch into the upstream the remote-tracking
// ref stands for (origin/main), as a pull request merge does: the branch's commits are
// NOT ancestors of upstream afterwards.
func squashInto(id, branch, main string) harness.Turn {
	return Bash(id, "git switch -q -c tmp-squash "+main+" && git merge --squash -q "+branch+
		" && git commit -q -m 'squash "+branch+"' && git update-ref refs/remotes/origin/main HEAD"+
		" && git switch -q "+main+" && git branch -q -D tmp-squash")
}

// T003_42: a branch the session committed on that was later squash-merged upstream is not
// judged again (its changes are all upstream already); one only partly landed is. A
// type-change commit (a file turned into a symlink) in the range is read, not a crash.
func TestT003_42_ASquashMergedBranchIsNotJudgedAndAPartlyLandedOneIs(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	e.Git(proj, "update-ref", "refs/remotes/origin/main", "HEAD")

	e.Run(proj, "s-003-42", "branches", Turns("done",
		Bash("b1", "git switch -q -c landed"),
		harness.CommitFile("c1", "docs/landed.md", "FORBIDDEN but reviewed upstream", "add landed"),
		Bash("b2", "rm notes/scratch.md && ln -s ../docs/seed.md notes/scratch.md && git add -A && git commit -q -m 'file to symlink'"),
		Bash("b3", "git switch -q "+main),
		squashInto("s1", "landed", main),
	))
	if got := stopRefusals(e, proj, "s-003-42"); got != "" {
		t.Fatalf("a branch whose changes are all upstream was judged again:\n%s", got)
	}
	blocks := stopBlocks(e, proj, "s-003-42")

	e.Run(proj, "s-003-42", "partly landed", Turns("done",
		Bash("b4", "git switch -q -c partial"),
		harness.CommitFile("c2", "docs/one.md", "clean words", "add one"),
		harness.CommitFile("c3", "docs/two.md", "FORBIDDEN words", "add two"),
		Bash("b5", "git switch -q "+main),
		// Only docs/one.md lands upstream.
		Bash("s2", "git switch -q -c tmp-squash "+main+" && git checkout -q partial -- docs/one.md && git commit -q -m 'land one' && git update-ref refs/remotes/origin/main HEAD && git switch -q "+main+" && git branch -q -D tmp-squash"),
	))
	got := newBlocks(e, proj, "s-003-42", blocks)
	if !strings.Contains(got, "partial") || !strings.Contains(got, refusalText) {
		t.Fatalf("a branch only partly landed upstream was not judged:\n%s", got)
	}
}
