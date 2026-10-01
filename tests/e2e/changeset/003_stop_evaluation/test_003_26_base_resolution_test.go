package e2e

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"os/exec"
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

// T003_29: the session-start commit is rewritten (an amend, a soft reset that recommits,
// a rebase onto new upstream work), then a violating commit X and a commit Y touching
// .sloprail land. The floor (Y's parent) is after X and the session start is gone, but its
// merge base with HEAD is not: X is still judged and refused. The session start the
// engine keeps is the FIRST one, so the Stop-time baseline re-take cannot move it past X.
func TestT003_29_ARewrittenSessionStartDoesNotLetAViolationPastTheFloor(t *testing.T) {
	rewrites := map[string]func(e *Env, proj string){
		"amend": func(e *Env, proj string) {
			e.Git(proj, "commit", "-q", "--amend", "--allow-empty", "-m", "the rule, rewritten")
		},
		"soft reset": func(e *Env, proj string) {
			e.Git(proj, "reset", "-q", "--soft", "HEAD~1")
			e.Git(proj, "commit", "-q", "-m", "the rule, recommitted")
		},
		"rebase": func(e *Env, proj string) {
			e.Git(proj, "checkout", "-q", "-b", "upstream", "HEAD~1")
			e.WriteFile(proj, "upstream.txt", "someone else's work\n")
			e.CommitAll(proj, "upstream work")
			e.Git(proj, "checkout", "-q", "main")
			e.Git(proj, "rebase", "-q", "upstream")
		},
	}
	for name, rewrite := range rewrites {
		t.Run(name, func(t *testing.T) {
			e, proj, led := project(t, docsRule)
			sess := "s-003-29-" + strings.ReplaceAll(name, " ", "-")
			e.Run(proj, sess, "hello", Turns("done", Bash("b1", "true")))
			e.RemoveCheckResults(proj, sess)
			start := e.Git(proj, "rev-parse", "HEAD")

			rewrite(e, proj)
			if err := exec.Command("git", "-C", proj, "merge-base", "--is-ancestor", start, "HEAD").Run(); err == nil {
				t.Fatalf("premise: the %s did not rewrite the session start", name)
			}
			e.WriteFile(proj, "docs/bad.md", "FORBIDDEN words\n")
			violation := e.CommitAll(proj, "X: the violation")
			e.WriteFile(proj, ".sloprail/lib/shared.sh", "#!/bin/sh\n# touched\n")
			e.CommitAll(proj, "Y: touch .sloprail")

			r := e.StopNow(proj, sess, false)
			if !strings.Contains(r.Output, "FORBIDDEN text in the changeset") {
				t.Fatalf("a violation after a rewritten session start (%s) and a .sloprail touch was not refused:\n%s", name, r.Output)
			}
			if !strings.Contains(r.Output, "docs/bad.md") {
				t.Fatalf("the refusal does not name the file it is about:\n%s", r.Output)
			}
			var judged []string
			for _, run := range ledger(t, led) {
				judged = append(judged, paths(run.Files)...)
			}
			if !strings.Contains(strings.Join(judged, " "), "docs/bad.md") {
				t.Fatalf("X's file was never handed to the rule: %v", judged)
			}

			// Fixed by a follow-up revert of the violation, the same range passes.
			e.Git(proj, "revert", "--no-edit", violation)
			if r := e.StopNow(proj, sess, false); harness.Blocked(r) {
				t.Fatalf("the range with the violation reverted was still refused (%s):\n%s", name, r.Output)
			}
		})
	}
}

// T003_31: the ROOT commit is amended to carry a violation. The session start (the old
// root) is gone and shares no history with the new one, so there is no merge base: the
// range starts at the empty tree and the amended root's own content is judged. (Anchored
// at the root commit instead, its content would be grandfathered and the violation would
// pass.) The rule is not committed, so there is no floor to fall back on either.
func TestT003_31_AnAmendedRootCommitIsJudgedInFull(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project")
	led := filepath.Join(t.TempDir(), "ledger.jsonl")
	e.FileGuard(proj, "docs", docsRule, map[string]string{"check.sh": recorder(led)}) // not committed: no floor

	const sess = "s-003-31"
	e.Run(proj, sess, "hello", Turns("done", Bash("b1", "true")))
	e.RemoveCheckResults(proj, sess)

	e.WriteFile(proj, "docs/bad.md", "FORBIDDEN words\n")
	e.Git(proj, "add", "docs/bad.md")
	e.Git(proj, "commit", "-q", "--amend", "--no-edit")

	r := e.StopNow(proj, sess, false)
	if !strings.Contains(r.Output, "FORBIDDEN text in the changeset") || !strings.Contains(r.Output, "docs/bad.md") {
		t.Fatalf("a violation inside an amended root commit was not refused:\n%s", r.Output)
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

// T003_28: a rule added mid-session applies from its own add commit: the work the
// session did before the rule existed is grandfathered (see T003_34 for the refusal side).
func TestT003_28_ARuleAddedMidSessionDoesNotJudgeTheSessionsEarlierWork(t *testing.T) {
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
	if harness.Blocked(r) {
		t.Fatalf("work made before the rule existed was judged by it:\n%s", r.Output)
	}
}

// T003_32: a repository with NO commit at session start. The session began before the first
// commit, so the commits the agent makes in its first turn are judged (the range starts at
// git's empty tree), not taken for where the session began. The first Stop refuses the
// violation; reverting it passes.
func TestT003_32_ASessionThatBeganBeforeTheFirstCommitJudgesItsFirstTurn(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := filepath.Join(t.TempDir(), "ledger.jsonl")
	e.FileGuard(proj, "docs", docsRule, map[string]string{"check.sh": recorder(led)}) // not committed

	const sess = "s-003-32"
	e.Run(proj, sess, "write the docs", Turns("done",
		harness.CommitFile("c1", "docs/bad.md", "FORBIDDEN words\n", "add bad"),
	))
	if joined := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n"); !strings.Contains(joined, "FORBIDDEN text in the changeset") || !strings.Contains(joined, "docs/bad.md") {
		t.Fatalf("a violation committed in the first turn of a session that began unborn was not refused:\n%s", joined)
	}
	seen := len(e.StopContinuations(proj, sess))

	e.Run(proj, sess, "fix it", Turns("done", Bash("rv", "git revert --no-edit HEAD")))
	if n := len(e.StopContinuations(proj, sess)); n != seen {
		t.Fatalf("the reverted range was still refused:\n%s", strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n"))
	}
}

// T003_33: a session recorded by an older engine (a baseline, but no kept first start)
// does not wedge: the re-taken baseline is not the start (it would reopen the floor hole),
// so the start is derived from the HEAD reflog at the record's first timestamp, kept, and
// the range is judged from it. (When the reflog cannot say, the range still fails closed,
// with a recovery: see T003_60.)
func TestT003_33_ASessionWithoutAKeptStartDerivesItFromTheReflog(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	const sess = "s-003-33"
	e.Run(proj, sess, "hello", Turns("done", Bash("b1", "true")))
	e.RemoveCheckResults(proj, sess)
	e.DeleteMeta(proj, sess, "session_start_commit")
	e.WriteFile(proj, "docs/a.md", "FORBIDDEN words\n")
	e.CommitAll(proj, "add a")

	r := e.StopNow(proj, sess, false)
	if !harness.Blocked(r) || strings.Contains(r.Output, "did not keep the commit it began at") ||
		!strings.Contains(r.Output, "FORBIDDEN text in the changeset") || !strings.Contains(r.Output, "docs/a.md") {
		t.Fatalf("a session without a kept start did not derive it and judge its commit:\n%s", r.Output)
	}
	if e.Meta(proj, sess, "session_start_commit") == "" {
		t.Fatal("the derived start was not kept")
	}
}
