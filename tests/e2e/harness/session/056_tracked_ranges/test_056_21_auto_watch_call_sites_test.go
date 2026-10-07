package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T056_21: every place the engine attached refs on its own is gated by SR_AUTO_WATCH_GIT_REFS
// (hooks, a Bash call that commits in another repository or adds a worktree, a sub-agent's Stop,
// the root's Stop). With the variable unset no auto row lands and the Stop is silent; what other
// readers need (commit-required, `refs track`/`untrack`, the Stop's verification of what was
// tracked) still works. The variable-on twin shows where behaviour differs.
//
// Citation: "we need e2e covering those w/ disabled watch behaviour".

func autoRows(t *testing.T, e *Env, proj, sess string) []sessionstate.TrackedRange {
	t.Helper()
	var out []sessionstate.TrackedRange
	for _, r := range ranges(t, e, proj, sess) {
		if r.Tracked() && r.AddedBy == sessionstate.RangeAuto {
			out = append(out, r)
		}
	}
	return out
}

// guardedOther is a second repository with the judged rule, so a range there would be tracked.
func guardedOther(e *Env) string {
	other := e.Project()
	e.GitInit(other)
	e.FileGuard(other, "docs", judgeRule, map[string]string{"rubric.md.j2": rubric})
	e.CommitAll(other, "the rule")
	return other
}

func TestT056_21_ACommitInAnotherRepositoryIsNotAutoWatchedUnlessOptedIn(t *testing.T) {
	for _, on := range []bool{false, true} {
		e, proj := unwatchedFailingProject(t)
		e.SetAutoWatch(on)
		other := guardedOther(e)
		sess := "s-056-21a-off"
		if on {
			sess = "s-056-21a-on"
		}
		e.Run(proj, sess, "work elsewhere", Turns("done", Bash("b1", "git -C "+other+" commit -q --allow-empty -m x")))

		rows := autoRows(t, e, proj, sess)
		if !on && (len(rows) != 0 || len(e.AllBlockingErrorsFrom(proj, sess, "Stop")) != 0) {
			t.Fatalf("off: auto rows %+v, Stop said %v", rows, e.AllBlockingErrorsFrom(proj, sess, "Stop"))
		}
		if on && len(rows) == 0 {
			t.Fatal("on: the other repository's branch was not auto-watched")
		}
	}
}

func TestT056_21_AWorktreeAddedByBashIsNotAutoWatchedUnlessOptedIn(t *testing.T) {
	for _, on := range []bool{false, true} {
		e, proj := unwatchedFailingProject(t)
		e.SetAutoWatch(on)
		wt := t.TempDir() + "/wt"
		sess := "s-056-21b-off"
		if on {
			sess = "s-056-21b-on"
		}
		e.Run(proj, sess, "branch out", Turns("done",
			Bash("b1", "git worktree add -q -b side "+wt),
			Bash("b2", "git -C "+wt+" commit -q --allow-empty -m side"),
		))

		rows := autoRows(t, e, proj, sess)
		if !on && (len(rows) != 0 || len(e.AllBlockingErrorsFrom(proj, sess, "Stop")) != 0) {
			t.Fatalf("off: auto rows %+v, Stop said %v", rows, e.AllBlockingErrorsFrom(proj, sess, "Stop"))
		}
		if on && len(rows) == 0 {
			t.Fatal("on: nothing was auto-watched")
		}
	}
}

func TestT056_21_ASubagentsRangeIsNotAutoWatchedUnlessOptedIn(t *testing.T) {
	for _, on := range []bool{false, true} {
		e, proj := unwatchedFailingProject(t)
		e.SetAutoWatch(on)
		sess := "s-056-21c-off"
		if on {
			sess = "s-056-21c-on"
		}
		runSubagentCommit(t, e, proj, sess)

		got := strings.Join(e.AllBlockingErrorsFrom(proj, sess, "Stop"), "\n")
		if !on && (len(autoRows(t, e, proj, sess)) != 0 || got != "" || !e.NoSubagentStopBlock(proj, sess)) {
			t.Fatalf("off: auto rows %+v, root Stop said %q, sub-agent blocked %v", autoRows(t, e, proj, sess), got, !e.NoSubagentStopBlock(proj, sess))
		}
		if on && !strings.Contains(got, failedText) {
			t.Fatalf("on: the root's Stop did not verify the sub-agent's range:\n%s", got)
		}
	}
}

// Kept: commit-required reads the registered folders and the rules, never the tracked rows.
func TestT056_21_CommitRequiredStillRefusesUncommittedWorkWithNothingWatched(t *testing.T) {
	e, proj := unwatchedFailingProject(t)
	const sess = "s-056-21d"
	e.Run(proj, sess, "write the doc", Turns("done", harness.Write("w1", "docs/new.md", "new doc\n")))
	if len(harness.CommitRequired(e.BlockingErrorsFrom(proj, sess, "Stop"))) == 0 {
		t.Fatalf("an uncommitted guarded file did not refuse the Stop with nothing watched; blocking: %q", e.BlockingErrors(proj, sess))
	}
}

// Kept: an auto row an earlier run (variable on) left is untracked at the next Stop, so it stops
// being verified; the same stored FAIL refused the earlier Stop.
func TestT056_21_LeftoverAutoRowsStopBeingVerifiedWhenTheVariableIsOff(t *testing.T) {
	e, proj := unwatchedFailingProject(t)
	e.SetAutoWatch(true)
	const sess = "s-056-21e"
	e.Run(proj, sess, "write the doc", Turns("done", harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a")))
	if got := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n"); !strings.Contains(got, failedText) {
		t.Fatalf("premise: the auto-watched failing range was not refused at Stop:\n%s", got)
	}
	if len(autoRows(t, e, proj, sess)) == 0 {
		t.Fatal("premise: no auto row")
	}

	e.SetAutoWatch(false)
	before := len(e.BlockingErrorsFrom(proj, sess, "Stop"))
	e.Run(proj, sess, "anything else?", Turns("done", Bash("b2", "true")))
	if got := e.BlockingErrorsFrom(proj, sess, "Stop"); len(got) != before {
		t.Fatalf("a leftover auto row was still verified with the variable off:\n%s", strings.Join(got[before:], "\n"))
	}
	if rows := autoRows(t, e, proj, sess); len(rows) != 0 {
		t.Fatalf("leftover auto rows are still tracked: %+v", rows)
	}
}
