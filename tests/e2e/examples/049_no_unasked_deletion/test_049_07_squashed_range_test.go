package e2e

// The commit-based after-check judges the SQUASHED range: every commit of the
// explicit range, as one diff from the range's base to HEAD. What the range's
// history did in between is not the question; its net result is — and that is a
// user decision (a deletion the agent put back is no deletion; one it left is).

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

const deleteTopic = `python3 -c "import os; os.remove('memories/topic.md')"`

// T049_23: a memory deleted in one commit and restored, byte for byte, by a later
// fix in the same range nets to nothing removed: the range passes, though the
// deletion cited no one's words. The script delete is invisible to the gate, so the
// after-check is what sees it — refused while the memory is gone, admitted once it
// is back.
func TestT049_23_DeletionRestoredInTheRangePasses(t *testing.T) {
	e := newEnvUncited(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — nothing net was removed"}`)
	seedCommittedMemory(t, e, proj, "memories/topic.md", "a fact worth keeping\n")

	const sess = "s-049-23"
	e.Run(proj, sess, "tidy the memories", Turns("done",
		Bash("d1", deleteTopic),
	).ThenCommit("drop the topic memory"))
	if e.Exists(proj, "memories/topic.md") {
		t.Fatalf("the script delete did not land, so this no longer tests the after-check")
	}
	blocks := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	if !strings.Contains(blocks, "preserves-unasked-content") || !strings.Contains(blocks, "must cite the user's own words") {
		t.Fatalf("an uncited committed deletion was not refused at Stop:\n%s", blocks)
	}
	refused := len(e.AllBlockingErrorsFrom(proj, sess, "Stop"))

	e.Run(proj, sess, "that memory was needed", Turns("done",
		Write("w1", "memories/topic.md", "a fact worth keeping\n"),
	).ThenCommit("restore the topic memory"))
	if got := len(e.AllBlockingErrorsFrom(proj, sess, "Stop")); got != refused {
		t.Fatalf("a deletion restored within the range was still refused (%d refusals, had %d):\n%s",
			got, refused, strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n"))
	}
}

// T049_24: a deletion never restored stays refused however many unrelated commits
// pile on top of it: the range's base does not move until the rule passes. Only a
// cited deletion (the user's own words, on the commit) is admitted, and then it goes
// to the judge like any removal.
func TestT049_24_DeletionNeverRestoredStaysRefused(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the user asked to drop this memory"}`)
	seedCommittedMemory(t, e, proj, "memories/topic.md", "a fact worth keeping\n")

	const sess = "s-049-24"
	e.Run(proj, sess, "drop the topic memory", Turns("done",
		Bash("d1", deleteTopic),
	).ThenCommit("drop the topic memory"))
	afterDelete := len(e.AllBlockingErrorsFrom(proj, sess, "Stop"))
	if afterDelete == 0 {
		t.Fatalf("an uncited committed deletion did not refuse the Stop")
	}

	e.Run(proj, sess, "add an unrelated memory", Turns("done",
		Write("w1", "memories/other.md", "another fact\n"),
	).ThenCommit("add another memory"))
	afterUnrelated := len(e.AllBlockingErrorsFrom(proj, sess, "Stop"))
	if afterUnrelated <= afterDelete {
		t.Fatalf("the unrestored deletion stopped being refused after an unrelated commit (%d, had %d)", afterUnrelated, afterDelete)
	}

	// A citation grounds the files its own commit changed, so the deletion is cited by
	// folding the commits since into one that carries the user's words.
	e.Run(proj, sess, "cite it", Turns("done", harness.SquashLast("squash", 2, "drop the topic memory", harness.CitesUser("drop the topic memory"))))
	if got := len(e.AllBlockingErrorsFrom(proj, sess, "Stop")); got != afterUnrelated {
		t.Fatalf("a deletion whose commit cites the user's words was still refused (%d, had %d):\n%s",
			got, afterUnrelated, strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n"))
	}
}
