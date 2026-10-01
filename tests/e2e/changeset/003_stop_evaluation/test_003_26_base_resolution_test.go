package e2e

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"path/filepath"
	"strings"
	"testing"
)

// Base resolution: the rule's watermark (a pass at ANY definition of the rule), else
// the earlier of the rule's floor and the session's start. Nothing made in the
// session is skipped, and approved work is not judged again.

// T003_26: a violating commit X, then a commit Y touching .sloprail (a shared lib, not
// the rule's own folder) before Stop. The floor moves to Y's parent, past X, but the
// session began before X, so X is still judged and refused.
func TestT003_26_TouchingSloprailAfterAViolationDoesNotSkipIt(t *testing.T) {
	e, proj, led := project(t, docsRule)
	e.Run(proj, "s-003-26", "hello", Turns("done", Bash("b1", "true")))
	e.RemoveCheckResults(proj, "s-003-26") // no pass recorded: the base is the floor / session start

	e.WriteFile(proj, "docs/bad.md", "FORBIDDEN words\n")
	e.CommitAll(proj, "X: the violation")
	e.WriteFile(proj, ".sloprail/lib/shared.sh", "#!/bin/sh\n# touched\n")
	e.CommitAll(proj, "Y: touch .sloprail")

	r := e.StopNow(proj, "s-003-26", false)
	if !strings.Contains(r.Output, "FORBIDDEN text in the changeset") {
		t.Fatalf("a violation followed by a commit under .sloprail was not refused:\n%s", r.Output)
	}
	var judged []string
	for _, run := range ledger(t, led) {
		judged = append(judged, paths(run.Files)...)
	}
	if !strings.Contains(strings.Join(judged, " "), "docs/bad.md") {
		t.Fatalf("X's file was never handed to the rule: %v", judged)
	}
}

// T003_28: the session-start commit is rewritten (an amend), then a violating commit X
// and a commit Y touching .sloprail land. The floor (Y's parent) is after X and the
// session start is gone, so neither can anchor the range. The merge base of HEAD with
// the remote's branch does: X is still judged and refused. With no remote branch to
// anchor on, the Stop refuses rather than guess which commits are new.
func TestT003_28_ARewrittenSessionStartDoesNotLetAViolationPastTheFloor(t *testing.T) {
	e, proj, led := project(t, docsRule)
	preSession := e.Git(proj, "rev-parse", "HEAD~1")
	e.Run(proj, "s-003-28", "hello", Turns("done", Bash("b1", "true")))
	e.RemoveCheckResults(proj, "s-003-28")

	e.Git(proj, "commit", "-q", "--amend", "--allow-empty", "-m", "the rule, rewritten")
	e.WriteFile(proj, "docs/bad.md", "FORBIDDEN words\n")
	e.CommitAll(proj, "X: the violation")
	e.WriteFile(proj, ".sloprail/lib/shared.sh", "#!/bin/sh\n# touched\n")
	e.CommitAll(proj, "Y: touch .sloprail")

	r := e.StopNow(proj, "s-003-28", false)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "can't tell which commits are new") {
		t.Fatalf("with the session start rewritten and no remote branch the Stop did not fail closed:\n%s", r.Output)
	}

	e.Git(proj, "update-ref", "refs/remotes/origin/main", preSession)
	r = e.StopNow(proj, "s-003-28", false)
	if !strings.Contains(r.Output, "FORBIDDEN text in the changeset") {
		t.Fatalf("a violation after a rewritten session start and a .sloprail touch was not refused:\n%s", r.Output)
	}
	var judged []string
	for _, run := range ledger(t, led) {
		judged = append(judged, paths(run.Files)...)
	}
	if !strings.Contains(strings.Join(judged, " "), "docs/bad.md") {
		t.Fatalf("X's file was never handed to the rule: %v", judged)
	}
}

// T003_27: a rule edited after a pass keeps its watermark: only the commits after the
// pass are judged (by the new rule), not what was approved before it.
func TestT003_27_ARuleEditedAfterAPassJudgesOnlyWhatComesAfter(t *testing.T) {
	e, proj, led := project(t, docsRule)
	e.Run(proj, "s-003-27", "write a", Turns("done", harness.CommitFile("c1", "docs/a.md", "clean\n", "add a")))
	if errs := e.BlockingErrorsFrom(proj, "s-003-27", "Stop"); len(errs) != 0 {
		t.Fatalf("the clean range was refused: %q", errs)
	}
	passed := e.Git(proj, "rev-parse", "HEAD")

	e.FileGuard(proj, "docs", docsRule, map[string]string{"check.sh": recorder(led) + "# the rule, edited\n"})
	e.CommitAll(proj, "edit the rule")
	e.Run(proj, "s-003-27", "write b", Turns("done", harness.CommitFile("c2", "docs/b.md", "clean\n", "add b")))

	runs := ledger(t, led)
	last := runs[len(runs)-1]
	if last.Base != passed {
		t.Fatalf("the edited rule was judged from %s, want from the pass at %s", last.Base, passed)
	}
	if got := paths(last.Files); len(got) != 1 || got[0] != "docs/b.md" {
		t.Fatalf("files = %v, want only docs/b.md (docs/a.md was approved before the edit)", got)
	}
}

// T003_28: a rule added mid-session judges from the EARLIER of its floor and the
// session's start: the work the session did before the rule existed is judged too.
func TestT003_28_ARuleAddedMidSessionJudgesTheSessionsEarlierWork(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project before the rule")
	e.Run(proj, "s-003-28", "hello", Turns("done", Bash("b1", "true")))

	e.WriteFile(proj, "docs/bad.md", "FORBIDDEN words\n")
	e.CommitAll(proj, "work before the rule exists")
	led := filepath.Join(t.TempDir(), "ledger.jsonl")
	e.FileGuard(proj, "docs", docsRule, map[string]string{"check.sh": recorder(led)})
	e.CommitAll(proj, "add the rule")

	r := e.StopNow(proj, "s-003-28", false)
	if !strings.Contains(r.Output, "FORBIDDEN text in the changeset") {
		t.Fatalf("work made earlier in the session was skipped by a rule added after it:\n%s", r.Output)
	}
}
