package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_63: an owed tip survives git's housekeeping. The branch holding an unjudged violating
// commit is deleted, its reflog expired and the repository garbage-collected with
// --prune=now: nothing but the engine's own pin holds the commit. Stop still judges and
// refuses it. And once a rule has passed the tip, the pin is dropped, so the engine leaves
// nothing behind.
func TestT003_63_AnOwedTipSurvivesBranchDeletionAndGarbageCollection(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	const sess = "s-003-63"

	e.Run(proj, sess, "commit, delete the branch, collect garbage", Turns("done",
		Bash("b1", "git switch -q -c side"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("b2", "git rev-parse HEAD > .git/owed-tip && git switch -q "+main),
		Bash("b3", "git branch -D side"),
		Bash("b4", "git reflog expire --expire=now --all && git gc -q --prune=now"),
	))
	if out := e.Git(proj, "cat-file", "-t", strings.TrimSpace(readLedger(t, proj+"/.git/owed-tip"))); out != "commit" {
		t.Fatalf("premise: the engine's pin should have kept the commit through the collection, got %q", out)
	}
	got := stopRefusals(e, proj, sess)
	if !strings.Contains(got, refusalText) || !strings.Contains(got, "docs/a.md") {
		t.Fatalf("a violation whose branch was deleted and whose objects were collected escaped; refusals:\n%s", got)
	}
	if pins := e.Git(proj, "for-each-ref", "refs/sloprail/pins"); pins == "" {
		t.Fatal("an owed tip should be pinned under refs/sloprail/pins")
	}
	blocks := stopBlocks(e, proj, sess)

	// Brought back and fixed, the tip passes, and its pin goes.
	e.Run(proj, sess, "bring it back and fix it", Turns("fixed",
		Bash("f1", "git switch -q -c side $(cat .git/owed-tip)"),
		harness.CommitFile("f2", "docs/a.md", "clean words", "fix a"),
		Bash("f3", "git switch -q "+main),
	))
	if n := stopBlocks(e, proj, sess); n != blocks {
		t.Fatalf("the restored and fixed branch was still refused:\n%s", newBlocks(e, proj, sess, blocks))
	}
	e.Run(proj, sess, "stop again", Turns("done", Bash("n1", "true")))
	if pins := e.Git(proj, "for-each-ref", "refs/sloprail/pins"); strings.TrimSpace(pins) != "" {
		t.Fatalf("a settled tip's pin was left behind:\n%s", pins)
	}
}
