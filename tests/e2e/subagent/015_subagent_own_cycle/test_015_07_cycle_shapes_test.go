package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The shapes a sub-agent's own cycle comes in, and what each leaves behind.
//
// Everything here is about the CYCLE rather than the dispatch — what the
// sub-agent's own stop did with what the sub-agent actually changed. 014 covers
// the dispatch shapes (how many, which tree); these cover what happens at the
// far end of one, which is the half that was unreachable before Bash was found
// to be applied inside a bound worktree.

// refusesOnceThenRelents refuses the first cycle it judges and permits every
// cycle after.
//
// The counter is a file in the file-guard's own folder ($SR_GUARDRAIL_DIR), which
// for an isolated sub-agent is the folder inside ITS worktree — so the count is
// per-sub-agent and a second sub-agent does not inherit the first's.
//
// A file-guard after-check (preventive omitted): it fires at the sub-agent's
// SubagentStop against the settled `.md` file it made, and a refusal blocks that
// stop the same as the old Post hook did. New-format refusal contract: exit
// non-zero with the reason as `{"reason":"…"}` on stdout (scriptRefusalReason
// prefers structured stdout), replacing the old exit-2-with-stderr.
const refusesOnceThenRelents = `match: "**/*.md"
checks:
  - script: ./record.sh
`

const refuseOnceScript = `#!/bin/sh
payload=$(cat)
path=$(printf '%s' "$payload" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')
n=$(cat "$SR_GUARDRAIL_DIR/count" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$SR_GUARDRAIL_DIR/count"
echo "call $n path=[$path]" >> "$SR_GUARDRAIL_DIR/log"
if [ "$n" -le 1 ]; then
  echo '{"reason":"the first attempt is refused"}'
  exit 1
fi
exit 0
`

// T015_07: a refusal at a sub-agent's own stop blocks it, reaches the record,
// and the sub-agent is sent round again rather than trapped.
//
// The multi-turn case the brief asks for, and the one with a real deadlock on
// the other side of it. SubagentStop treats a refusal as a block and re-runs the
// sub-agent's turn; a rule refusing on a condition the retry cannot change turns
// "this work is refused" into "this sub-agent can never finish".
//
// So a rule that refuses ONCE is the discriminating shape. Three things must all
// hold, and they fail independently:
//
//   - the refusal is recorded against the SUB-AGENT'S own stop, and the
//     sub-agent's own work never turns up in the ROOT's verdicts (it is in a
//     worktree the root's cycle does not diff);
//   - the sub-agent goes round again — the retry loop is driven;
//   - and it FINISHES, rather than running to the harness's cap.
//
// MEASURED, and it is the part worth knowing: on the retry the guardrail runs
// exactly ONCE more in total, not once per file. `stop_hook_active` is set on
// the re-fired stop and subagent-stop returns immediately without judging
// anything — so the cycle that follows a refusal does no work at all. That is
// the documented behaviour of the StopHookActive branch, and this is what it
// looks like from outside.
func TestT015_07_ARefusedSubagentCycleRetriesAndThenFinishes(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "onceonly", refusesOnceThenRelents, map[string]string{"record.sh": refuseOnceScript})
	e.GitInit(proj)

	sub := subScenario(t, harness.Turns("sub done",
		Bash("sb1", "echo one > first.md"),
		Bash("sb2", "echo two > second.md"),
	))

	res := e.Run(proj, "s-015-07", "delegate work that gets refused once", Turns("root done",
		Dispatch("d1", "do the job", sub, "worktree"),
	))

	// It FINISHED. This is the deadlock check and it comes first: everything
	// else is about a cycle that got to happen.
	if hitRetryCap(res.Output) {
		t.Fatalf("the sub-agent was driven to the harness's retry cap. A rule that refuses a "+
			"delegated cycle must leave the sub-agent able to finish — a guardrail engine that "+
			"bricks delegation is worse than one that stays quiet:\n%s", res.Output)
	}
	if !res.Saw("root done") {
		t.Fatalf("the dispatching session never completed:\n%s", res.Output)
	}

	// The refusal really happened, at the SUB-AGENT's own stop.
	blocking := e.BlockingErrorsFrom(proj, "s-015-07", "SubagentStop")
	if len(blocking) == 0 {
		t.Fatalf("nothing was recorded as refused at the sub-agent's own cycle, so this test is "+
			"about a delegation nothing ever objected to:\n%s", res.Output)
	}
	told := strings.Join(blocking, "\n")
	if !strings.Contains(told, "the first attempt is refused") {
		t.Errorf("the refusal was recorded without its own words, so nothing can act on it:\n%s", told)
	}

	// And the sub-agent's own WORK stayed out of the root's cycle.
	//
	// Not "the root refused nothing", which is what this assertion first said
	// and which fails on a clean engine. The root's Stop does refuse here, and
	// legitimately: the same rule is bound in the project tree, where the
	// harness's own .scenario.sh and this guardrail's ledger files are
	// untracked changes the root's cycle correctly judges. Measured — the root's
	// verdicts name .scenario.sh and .sloprail/guardrails/..., never the
	// sub-agent's file.
	//
	// So the claim is the one that matters: the file the SUB-AGENT made is in a
	// tree the root never diffs, and no verdict of the root's is about it. A
	// root judging it would be the parent handed another session's work as its
	// own.
	for _, l := range e.FileGuardLedgerLines(proj, "onceonly", "log") {
		if pathOf(l) == "first.md" || pathOf(l) == "second.md" {
			t.Fatalf("the DISPATCHING session's own cycle judged %q — a file that exists only in "+
				"the sub-agent's separate worktree. The delegated work was attributed to the "+
				"session that dispatched it. Line: %s", pathOf(l), l)
		}
	}

	// The sub-agent was sent round again: the harness says so in its own words.
	if !strings.Contains(res.Output, "re-running subagent") {
		t.Fatalf("the sub-agent was never re-run after its cycle was refused. A Post refusal "+
			"cannot undo the write; the whole mechanism by which it gets anything corrected is "+
			"sending the agent round again:\n%s", res.Output)
	}

	// The guardrail ran twice in total across the two cycles — once to refuse,
	// and once more. Not once per file on the retry: the re-fired stop carries
	// stop_hook_active and subagent-stop returns without judging, which is the
	// StopHookActive branch doing exactly what it documents.
	lines := subLedger(t, proj, theWorktree(t, proj), "onceonly", "log")
	if len(lines) == 0 {
		t.Fatalf("the guardrail never ran at the sub-agent's cycle at all")
	}
	if len(lines) > 4 {
		t.Fatalf("the guardrail ran %d times (%v). A cycle already refused once must not be "+
			"judged again and again — that is the loop the sub-agent cannot leave", len(lines), lines)
	}
}

// T015_08: the same refusal, re-fired, does nothing — the StopHookActive guard,
// observed from outside.
//
// Its own test because the property is not "the sub-agent finished" but "the
// second stop judged NOTHING". A rule that refuses EVERY time is the shape that
// separates them: the first cycle refuses, the stop re-fires with
// stop_hook_active set, and subagent-stop must return immediately rather than
// refuse a second time.
//
// If it did not, the sub-agent would be refused on every retry until the
// harness's cap — which is what the guard exists to prevent and what this test
// would then see.
//
// The engine-side contract is TestSubagentStopHonoursStopHookActive; this is the
// same claim through a user's own wiring, where the retry is real rather than a
// payload field set by hand.
func TestT015_08_AReFiredSubagentStopJudgesNothingAgain(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "always", refusesEverything, map[string]string{"record.sh": refuseAlwaysScript})
	e.GitInit(proj)

	sub := subScenario(t, harness.Turns("sub done",
		Bash("sb1", "echo x > refused-work.md"),
	))

	res := e.Run(proj, "s-015-08", "delegate work that is always refused", Turns("root done",
		Dispatch("d1", "do the job", sub, "worktree"),
	))

	// The whole point: an always-refusing rule must not drive the sub-agent to
	// the cap. The guard is what stops the second cycle refusing again.
	if hitRetryCap(res.Output) {
		t.Fatalf("a rule refusing every cycle drove the sub-agent to the harness's retry cap. "+
			"The stop that re-fires carries stop_hook_active, and a cycle already refused once "+
			"must be left alone entirely — otherwise a refusal the retry cannot satisfy becomes a "+
			"loop the sub-agent cannot leave:\n%s", res.Output)
	}
	if !res.Saw("root done") {
		t.Fatalf("the dispatching session never completed:\n%s", res.Output)
	}

	// The rule did refuse, or the guard was never exercised.
	if len(e.BlockingErrorsFrom(proj, "s-015-08", "SubagentStop")) == 0 {
		t.Fatalf("nothing was refused, so the re-fired stop this test is about never happened:\n%s",
			res.Output)
	}

	// And the second cycle judged nothing. The rule refuses unconditionally, so
	// a second full cycle would append a second line — and then a third, and so
	// on to the cap.
	lines := subLedger(t, proj, theWorktree(t, proj), "always", "log")
	if len(lines) == 0 {
		t.Fatalf("the guardrail never ran at all, so there was no refusal and no re-fired stop")
	}
	if len(lines) > 2 {
		t.Fatalf("the guardrail ran %d times (%v) against one file. A cycle already refused once "+
			"is left alone entirely; judging it again is the retry loop this guard exists to "+
			"prevent", len(lines), lines)
	}
}

const refusesEverything = `match: "**/*.md"
checks:
  - script: ./record.sh
`

const refuseAlwaysScript = `#!/bin/sh
cat >/dev/null
echo "asked" >> "$SR_GUARDRAIL_DIR/log"
echo '{"reason":"this rule always says no"}'
exit 1
`

// T015_09: a sub-agent that changes nothing ends cleanly and judges nothing.
//
// The negative case that stops every other test in this package from being
// satisfied by an engine that fires guardrails indiscriminately. If a cycle with
// no difference still produced verdicts, "the sub-agent's file was judged" would
// carry no information — and a rule bound to created files would refuse a
// sub-agent that created none.
//
// Two shapes, because they fail differently: one that runs a command touching
// nothing, and one that creates a file and removes it again within the cycle. The
// second is the interesting one — the tree ends where it started, so a cycle
// diffing START against END correctly sees nothing, while one that tracked
// individual tool calls would see a creation.
func TestT015_09_ASubagentThatChangesNothingJudgesNothing(t *testing.T) {
	for _, tc := range []struct{ name, command string }{
		{"writes nothing at all", "true"},
		{"writes then deletes it again", "echo x > transient.md && rm transient.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New(t)
			proj := e.Project()
			e.FileGuard(proj, "recorder", recordsPathAndSession, map[string]string{"record.sh": recordScript})
			e.GitInit(proj)

			sub := subScenario(t, harness.Turns("sub done", Bash("sb1", tc.command)))

			res := e.Run(proj, "s-015-09-"+strings.ReplaceAll(tc.name, " ", "-"),
				"delegate work that leaves nothing", Turns("root done",
					Dispatch("d1", "do the job", sub, "worktree"),
				))

			if !res.Saw("root done") {
				t.Fatalf("the delegated cycle did not complete:\n%s", res.Output)
			}
			if hitRetryCap(res.Output) {
				t.Fatalf("a sub-agent that changed nothing was driven to the retry cap:\n%s", res.Output)
			}

			// The dispatch was real, so "nothing was judged" is about a cycle
			// that happened rather than one that never ran.
			wt := theWorktree(t, proj)

			if lines := subLedger(t, proj, wt, "recorder", "log"); len(lines) != 0 {
				t.Fatalf("a sub-agent whose cycle left the tree as it found it still had %d "+
					"verdict(s) recorded (%v). A cycle with no difference must judge nothing — "+
					"otherwise a rule bound to created files refuses a sub-agent that created "+
					"none, and every positive assertion in this package stops carrying "+
					"information", len(lines), lines)
			}

			// And the transient file really is gone, so the second case is the
			// case it claims to be.
			if tc.command != "true" {
				if _, err := os.Stat(filepath.Join(proj, ".claude", "worktrees", wt, "transient.md")); err == nil {
					t.Fatalf("the file the sub-agent deleted is still there, so the tree DID differ " +
						"and this case did not exercise what it says")
				}
			}
		})
	}
}

// T015_10: a sub-agent that COMMITS its work leaves its cycle nothing to judge.
//
// Worth pinning because it is surprising and because it is the honest answer.
// The difference a cycle judges is the tree against the session's baseline
// commit, so work that has been committed is no longer a difference — it is
// history. A sub-agent that commits therefore ends with an empty cycle, and the
// guardrails bound to created files never see what it made.
//
// MEASURED here rather than reasoned about: the sub-agent commits, and its
// worktree's ledger is empty.
//
// Recorded as behaviour, not endorsed as desirable. Whether committing should
// take work out of a guardrail's reach is a product question this test does not
// settle; what it does is stop the answer changing silently. A change making
// committed work visible to the cycle fails here and is noticed.
func TestT015_10_ASubagentThatCommitsLeavesItsCycleNothingToJudge(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "recorder", recordsPathAndSession, map[string]string{"record.sh": recordScript})
	e.GitInit(proj)

	// Identity given on the command line so the run does not depend on whatever
	// the machine has configured, and --no-gpg-sign so a signing setup cannot
	// make this hang.
	sub := subScenario(t, harness.Turns("sub done",
		Bash("sb1", "echo committed > committed-by-the-sub.md && git add -A && "+
			"git -c user.email=sub@example.invalid -c user.name=sub commit -q -m 'sub work' --no-gpg-sign"),
	))

	res := e.Run(proj, "s-015-10", "delegate work that gets committed", Turns("root done",
		Dispatch("d1", "do the job and commit it", sub, "worktree"),
	))

	if !res.Saw("root done") {
		t.Fatalf("the delegated cycle did not complete:\n%s", res.Output)
	}
	if hitRetryCap(res.Output) {
		t.Fatalf("the sub-agent hit the retry cap:\n%s", res.Output)
	}

	wt := theWorktree(t, proj)
	wtPath := filepath.Join(proj, ".claude", "worktrees", wt)

	// The commit really happened, or this is T015_09 with extra words.
	if _, err := os.Stat(filepath.Join(wtPath, "committed-by-the-sub.md")); err != nil {
		t.Fatalf("the sub-agent's file is not in its worktree, so the commit case was never "+
			"exercised: %v", err)
	}
	if out := e.Git(wtPath, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("the sub-agent's worktree still has uncommitted changes (%q), so its work was "+
			"never committed and this test is not about a committed cycle", out)
	}

	// The measured consequence: nothing for the cycle to judge.
	if lines := subLedger(t, proj, wt, "recorder", "log"); len(lines) != 0 {
		t.Fatalf("a sub-agent that COMMITTED its work still had %d verdict(s) recorded (%v). "+
			"Measured behaviour on this branch is that a cycle's difference is the tree against "+
			"the baseline commit, so committed work is history rather than a difference and the "+
			"cycle judges nothing. If that has deliberately changed, this test states the old "+
			"answer and should be re-derived rather than deleted", len(lines), lines)
	}
}
