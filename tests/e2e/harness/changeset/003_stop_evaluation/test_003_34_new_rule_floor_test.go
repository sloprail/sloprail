package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// A rule applies from the commit that added or changed it, with earlier history
// grandfathered, when it did not exist at session start. A rule that did exist keeps
// the earlier of its floor and the session start, so touching .sloprail (or deleting
// and re-adding the rule) is no way to skip judging earlier bad work.

// T003_34: three violating commits, THEN the rule is added. The first Stop judges only
// the add commit and later: the earlier violations are not reported. A violation
// committed after the rule is refused, and reverting it passes.
// sr:proves fileguard/rule-age-floor
func TestT003_34_ARuleAddedAfterViolationsJudgesOnlyFromItsAddCommit(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project before the rule")
	const sess = "s-003-34"
	e.Run(proj, sess, "hello", Turns("done", Bash("b1", "true")))

	for _, name := range []string{"one", "two", "three"} {
		e.WriteFile(proj, "docs/early-"+name+".md", "FORBIDDEN words\n")
		e.CommitAll(proj, "violation "+name+" before the rule exists")
	}
	led := filepath.Join(t.TempDir(), "ledger.jsonl")
	e.FileGuard(proj, "docs", docsRule, map[string]string{"check.sh": recorder(led)})
	e.CommitAll(proj, "add the rule")

	// Only the add commit and later: the three earlier violations are grandfathered.
	if r := e.StopJudged(proj, sess, false); harness.Blocked(r) {
		t.Fatalf("violations committed before the rule existed were reported:\n%s", r.Output)
	}

	// A violation after the rule is still refused, and only it is named.
	e.WriteFile(proj, "docs/late.md", "FORBIDDEN words\n")
	late := e.CommitAll(proj, "violation after the rule")
	r := e.StopJudged(proj, sess, false)
	if !strings.Contains(r.Output, "FORBIDDEN text in the changeset") || !strings.Contains(r.Output, "docs/late.md") {
		t.Fatalf("a violation committed after the rule was not refused:\n%s", r.Output)
	}
	if strings.Contains(r.Output, "early-") {
		t.Fatalf("the refusal names a file committed before the rule existed:\n%s", r.Output)
	}
	for _, run := range ledger(t, led) {
		if joined := strings.Join(paths(run.Files), " "); strings.Contains(joined, "early-") {
			t.Fatalf("a file from before the rule was handed to it: %v", paths(run.Files))
		}
	}

	e.Git(proj, "revert", "--no-edit", late)
	if r := e.StopJudged(proj, sess, false); harness.Blocked(r) {
		t.Fatalf("the reverted range was still refused:\n%s", r.Output)
	}
}

// T003_35: a rule that existed at session start, a bad commit, then a touch of ANOTHER
// .sloprail file: the bad commit is still judged. Reverting it passes.
func TestT003_35_ARuleFromSessionStartStillJudgesABadCommitBeforeASloprailTouch(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	const sess = "s-003-35"
	e.Run(proj, sess, "hello", Turns("done", Bash("b1", "true")))

	e.WriteFile(proj, "docs/bad.md", "FORBIDDEN words\n")
	bad := e.CommitAll(proj, "the violation")
	e.WriteFile(proj, ".sloprail/lib/shared.sh", "#!/bin/sh\n# touched\n")
	e.CommitAll(proj, "touch another .sloprail file")

	r := e.StopJudged(proj, sess, false)
	if !strings.Contains(r.Output, "FORBIDDEN text in the changeset") || !strings.Contains(r.Output, "docs/bad.md") {
		t.Fatalf("a bad commit followed by a .sloprail touch was not refused:\n%s", r.Output)
	}
	e.Git(proj, "revert", "--no-edit", bad)
	if r := e.StopJudged(proj, sess, false); harness.Blocked(r) {
		t.Fatalf("the reverted range was still refused:\n%s", r.Output)
	}
}

// T003_36: a rule deleted and re-added in the session existed at session start, so it
// keeps the strict range: the bad commit before the delete is still judged.
// sr:proves fileguard/rule-age-floor
func TestT003_36_ARuleDeletedAndReAddedStaysStrict(t *testing.T) {
	e, proj, led := project(t, docsRule)
	const sess = "s-003-36"
	e.Run(proj, sess, "hello", Turns("done", Bash("b1", "true")))

	e.WriteFile(proj, "docs/bad.md", "FORBIDDEN words\n")
	bad := e.CommitAll(proj, "the violation")
	e.Git(proj, "rm", "-rq", ".sloprail/file-guard/docs")
	e.Git(proj, "commit", "-q", "-m", "delete the rule")
	e.FileGuard(proj, "docs", docsRule, map[string]string{"check.sh": recorder(led)})
	e.CommitAll(proj, "re-add the rule")

	r := e.StopJudged(proj, sess, false)
	if !strings.Contains(r.Output, "FORBIDDEN text in the changeset") || !strings.Contains(r.Output, "docs/bad.md") {
		t.Fatalf("deleting and re-adding the rule skipped the earlier violation:\n%s", r.Output)
	}
	e.Git(proj, "revert", "--no-edit", bad)
	if r := e.StopJudged(proj, sess, false); harness.Blocked(r) {
		t.Fatalf("the reverted range was still refused:\n%s", r.Output)
	}
}

// T003_37: a rule committed long before the session (commit A), then commits B and C that
// violate it, and the session starts at C. With no watermark the base is the session
// start, never the rule's older floor: the first Stop judges only the session's own
// commit D. B and C are not reported; a violation in D is refused, and fixing it passes.
func TestT003_37_ARuleOlderThanTheSessionJudgesOnlyTheSessionsOwnCommits(t *testing.T) {
	e, proj, led := project(t, docsRule) // A: the rule
	for _, name := range []string{"b", "c"} {
		e.WriteFile(proj, "docs/before-"+name+".md", "FORBIDDEN words\n")
		e.CommitAll(proj, "merged before the session: "+name)
	}
	const sess = "s-003-37"
	e.Run(proj, sess, "hello", Turns("done", Bash("b1", "true"))) // the session starts at C

	e.WriteFile(proj, "docs/d.md", "FORBIDDEN words\n")
	e.CommitAll(proj, "D: the session's own violation")

	r := e.StopJudged(proj, sess, false)
	if !strings.Contains(r.Output, "FORBIDDEN text in the changeset") || !strings.Contains(r.Output, "docs/d.md") {
		t.Fatalf("the session's own violation was not refused:\n%s", r.Output)
	}
	if strings.Contains(r.Output, "before-") {
		t.Fatalf("the refusal names a commit merged before the session started:\n%s", r.Output)
	}
	for _, run := range ledger(t, led) {
		if got := strings.Join(paths(run.Files), " "); strings.Contains(got, "before-") {
			t.Fatalf("a file from before the session was handed to the rule: %v", paths(run.Files))
		}
	}

	e.WriteFile(proj, "docs/d.md", "clean words\n")
	e.CommitAll(proj, "fix D")
	if r := e.StopJudged(proj, sess, false); harness.Blocked(r) {
		t.Fatalf("the fixed range was still refused:\n%s", r.Output)
	}
}

// T003_38: a rule added mid-session in commit A, violating commits B and C after it, then a
// commit D touching another .sloprail file. The base is the parent of the commit that FIRST
// added the rule, not of D's predecessor: B and C are refused, then fixed, then pass.
// sr:proves fileguard/rule-age-floor
func TestT003_38_ALaterSloprailTouchDoesNotHideViolationsAfterTheRuleWasAdded(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project before the rule")
	const sess = "s-003-38"
	e.Run(proj, sess, "hello", Turns("done", Bash("b1", "true")))

	led := filepath.Join(t.TempDir(), "ledger.jsonl")
	e.FileGuard(proj, "docs", docsRule, map[string]string{"check.sh": recorder(led)})
	e.CommitAll(proj, "A: add the rule")
	e.WriteFile(proj, "docs/b.md", "FORBIDDEN words\n")
	e.CommitAll(proj, "B: violation")
	e.WriteFile(proj, "docs/c.md", "FORBIDDEN words\n")
	e.CommitAll(proj, "C: violation")
	e.WriteFile(proj, ".sloprail/lib/shared.sh", "#!/bin/sh\n# touched\n")
	e.CommitAll(proj, "D: touch another .sloprail file")

	r := e.StopJudged(proj, sess, false)
	if !strings.Contains(r.Output, "FORBIDDEN text in the changeset") || !strings.Contains(r.Output, "docs/b.md") || !strings.Contains(r.Output, "docs/c.md") {
		t.Fatalf("violations after the rule was added escaped a later .sloprail touch:\n%s", r.Output)
	}

	e.WriteFile(proj, "docs/b.md", "clean words\n")
	e.WriteFile(proj, "docs/c.md", "clean words\n")
	e.CommitAll(proj, "fix B and C")
	if r := e.StopJudged(proj, sess, false); harness.Blocked(r) {
		t.Fatalf("the fixed range was still refused:\n%s", r.Output)
	}
}
