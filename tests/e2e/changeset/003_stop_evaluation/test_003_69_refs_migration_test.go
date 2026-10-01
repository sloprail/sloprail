package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_69: a session whose store was written by an engine that kept no pins and no ref starts
// (a long-running one) is migrated by the first hook that opens it: its owed tips are pinned
// (so a branch deleted after that still survives garbage collection), nothing it owed is
// settled by the migration, and the store says what version it is at.
func TestT003_69_AStoreFromBeforePinsIsMigratedAndItsOwedTipsStayOwed(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	const sess = "s-003-69"

	e.Run(proj, sess, "commit on a branch and leave it", Turns("done",
		Bash("b1", "git switch -q -c side"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("b2", "git switch -q "+main),
		Bash("b3", "git rev-parse side > .git/owed-tip"),
	))
	if got := stopRefusals(e, proj, sess); !strings.Contains(got, refusalText) {
		t.Fatalf("premise: the branch's violation should be refused:\n%s", got)
	}
	blocks := stopBlocks(e, proj, sess)

	// Make the store the previous engine would have written: no version, no start, no pins.
	folder := e.SessionFolders(proj, sess)[0].Path
	e.DeleteMeta(proj, sess, "refs_schema")
	e.DeleteMeta(proj, sess, "ref_start:"+folder+"|refs/heads/side")
	for _, line := range strings.Split(e.Git(proj, "for-each-ref", "--format=%(refname)", "refs/sloprail/pins"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			e.Git(proj, "update-ref", "-d", line)
		}
	}
	if pins := strings.TrimSpace(e.Git(proj, "for-each-ref", "refs/sloprail/pins")); pins != "" {
		t.Fatalf("premise: the pins should be gone:\n%s", pins)
	}

	e.Run(proj, sess, "now delete it and collect garbage", Turns("done",
		Bash("d1", "git branch -D side"),
		Bash("d2", "git reflog expire --expire=now --all && git gc -q --prune=now"),
	))
	if got := e.Meta(proj, sess, "refs_schema"); got != "2" {
		t.Fatalf("the store was not migrated: refs_schema = %q", got)
	}
	if out := e.Git(proj, "cat-file", "-t", strings.TrimSpace(readLedger(t, proj+"/.git/owed-tip"))); out != "commit" {
		t.Fatalf("the migrated store's owed tip did not survive the collection, got %q", out)
	}
	got := newBlocks(e, proj, sess, blocks)
	if !strings.Contains(got, refusalText) || !strings.Contains(got, "docs/a.md") {
		t.Fatalf("the owed tip was settled by the migration or lost to the collection; refusals:\n%s", got)
	}
}
