package e2e

import (
	"strings"
	"testing"
)

// T003_01: the rule is evaluated over the committed range and refuses; the
// refusal reaches the agent. A fix commit is judged over the WHOLE range — the
// squashed net change, both commits — and passes; the watermark then sits at that
// head, so a Stop with nothing new runs no check, and a later commit is judged
// alone.
func TestT003_01_RefuseThenFixThenWatermark(t *testing.T) {
	e, proj, led := project(t, docsRule)
	floor := e.Git(proj, "rev-parse", "HEAD")

	// Refusal: a commit the rule objects to.
	e.Run(proj, "s-003-01", "write the doc", Turns("done",
		commit("c1", "docs/a.md", "FORBIDDEN words", "add a"),
	))
	errs := e.BlockingErrorsFrom(proj, "s-003-01", "Stop")
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "FORBIDDEN text in the changeset") {
		t.Fatalf("the rule did not refuse the committed range; blocking errors: %q", e.BlockingErrors(proj, "s-003-01"))
	}
	runs := ledger(t, led)
	if len(runs) == 0 {
		t.Fatal("the check never ran")
	}
	first := runs[0]
	if first.Kind != "Changeset" || first.Base != floor || !first.TreeSet ||
		first.EnvBase != first.Base || first.EnvHead != first.Head {
		t.Fatalf("the check was handed %+v; want a Changeset from the rule's floor with SR_TREE/SR_BASE/SR_HEAD set", first)
	}
	if got := paths(first.Files); len(got) != 1 || got[0] != "docs/a.md" || first.Files[0].Status != "A" {
		t.Fatalf("files = %+v", first.Files)
	}
	refusedRuns := len(runs)

	// The refused range does not advance: the fix is judged with the original
	// commit still in it — one squashed range, not the fix alone.
	e.Run(proj, "s-003-01", "fix it", Turns("fixed",
		commit("c2", "docs/a.md", "clean words", "fix a"),
	))
	runs = ledger(t, led)
	last := runs[len(runs)-1]
	if last.Base != floor || len(last.Commits) != 2 {
		t.Fatalf("after the fix the range was %+v; want both commits from the floor", last)
	}
	if len(runs) == refusedRuns {
		t.Fatal("the fix was not judged")
	}
	after := len(e.BlockingErrorsFrom(proj, "s-003-01", "Stop"))

	// Pass recorded: the watermark is the head it passed at. A Stop with nothing
	// new selects nothing and runs no check.
	passedHead := last.Head
	before := len(ledger(t, led))
	e.Run(proj, "s-003-01", "anything else?", Turns("no", Bash("b1", "true")))
	if n := len(ledger(t, led)); n != before {
		t.Fatalf("a Stop with no new commits ran the check %d more time(s)", n-before)
	}

	// A later commit is judged alone, from the watermark.
	e.Run(proj, "s-003-01", "one more", Turns("more", commit("c3", "docs/b.md", "more clean words", "add b")))
	runs = ledger(t, led)
	next := runs[len(runs)-1]
	if next.Base != passedHead || len(next.Commits) != 1 || next.Commits[0] != "add b" {
		t.Fatalf("the next range was %+v; want only the new commit, from %s", next, passedHead)
	}
	if n := len(e.BlockingErrorsFrom(proj, "s-003-01", "Stop")); n != after {
		t.Fatalf("a passing range was refused (%d blocking errors, had %d)", n, after)
	}
}

// T003_02: `match` selecting nothing in a computed range is a pass — and is
// recorded, so the watermark advances over it.
func TestT003_02_NothingSelectedIsAPass(t *testing.T) {
	e, proj, led := project(t, docsRule)
	e.Run(proj, "s-003-02", "take notes", Turns("done",
		commit("c1", "notes/more.md", "FORBIDDEN but unguarded", "add a note"),
	))
	if runs := ledger(t, led); len(runs) != 0 {
		t.Fatalf("the check ran on a range where match selected nothing: %+v", runs)
	}
	if errs := e.BlockingErrorsFrom(proj, "s-003-02", "Stop"); len(errs) != 0 {
		t.Fatalf("refused: %q", errs)
	}
	res := e.CLIDirectEnv(proj, []string{"CLAUDE_CODE_SESSION_ID=s-003-02", "CLAUDE_CONFIG_DIR=" + e.ConfigDir(), "CLAUDECODE="},
		"sr-checks", "status")
	if res.Code != 0 || !strings.Contains(res.Output, "file-guard/docs") || !strings.HasPrefix(strings.TrimSpace(res.Output), "pass") {
		t.Fatalf("the empty selection was not recorded as a pass:\n%s", res.Output)
	}
}
