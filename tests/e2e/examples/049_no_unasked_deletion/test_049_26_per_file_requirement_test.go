package e2e

// The citation requirement is evaluated PER FILE: removes-content.sh decides for the one
// file it is asked about, so a cited addition to one file does not make another file's
// uncited deletion look grounded, and an uncited addition is not asked for a citation at
// all — the refusal names only the file whose own change removed content.

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T049_26: an uncited deletion in notes.md and a cited addition in ideas.md, in one
// range, is refused naming ONLY notes.md — and so is it with an uncited addition in
// todo.md beside them, which removes nothing and so needs no citation. Following the refusal's own command (the
// range is squashed into one commit carrying the quote) then passes.
func TestT049_26_OnlyTheFileThatRemovedContentNeedsTheCitation(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the user asked to drop the notes"}`)
	e.WriteFile(proj, "memories/notes.md", "a fact worth keeping\n")
	e.WriteFile(proj, "memories/ideas.md", "first idea\n")
	e.CommitSeedThenRules(proj, "seed the memories")

	const ask = "drop the notes memory and note a second idea"
	const sess = "s-049-26"
	e.Run(proj, sess, ask, Turns("done",
		Bash("d1", `python3 -c "import os; os.remove('memories/notes.md')"`),
	).ThenCommit("drop the notes memory"))
	if e.Exists(proj, "memories/notes.md") {
		t.Fatalf("the script delete did not land, so this tests nothing")
	}
	e.Run(proj, sess, "and note a second idea", Turns("done",
		harness.CommitFile("c1", "memories/ideas.md", "first idea\nsecond idea\n", "note a second idea", harness.CitesUser(ask)),
		harness.CommitFile("c2", "memories/todo.md", "buy milk\n", "add a todo"),
	))
	refusals := e.AllBlockingErrorsFrom(proj, sess, "Stop")
	if len(refusals) == 0 {
		t.Fatalf("the uncited deletion was not refused at Stop")
	}
	refusal := refusals[len(refusals)-1]
	if got := harness.UngroundedFiles(refusal); got != "memories/notes.md" {
		t.Fatalf("the refusal names %q as not grounded, want only memories/notes.md:\n%s", got, refusal)
	}
	if !strings.Contains(refusal, "an empty commit carrying only the trailer does not count") {
		t.Errorf("the refusal does not say an empty trailer-only commit grounds nothing:\n%s", refusal)
	}
	refused := len(e.StopContinuations(proj, sess))

	// The deletion's commit is not HEAD (the cited addition is), so the refusal's one
	// command is the squash: the range as one commit carrying the quote.
	e.Run(proj, sess, "go on", Turns("done",
		harness.RefusalCommand(t, "fix", refusal, "git reset --soft", ask),
	))
	if got := len(e.StopContinuations(proj, sess)); got != refused {
		t.Fatalf("the refusal's own command did not ground the deletion (%d refusals, had %d):\n%s",
			got, refused, strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n"))
	}
}
