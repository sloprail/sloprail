package e2e

import (
	"fmt"
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
// The counter is a file at the root of the tree the check runs in (three levels above
// $SR_GUARDRAIL_DIR), which for an isolated sub-agent is ITS worktree — so the count is
// per-sub-agent and a second sub-agent does not inherit the first's. Not inside the
// guard's folder: a verdict is keyed by a hash of everything under `.sloprail`, so a check
// writing there changes its own key between the run and the verify, and the file would be
// an uncommitted guarded change at the Stop.
//
// A file-guard after-check (a file-guard acts only at Stop): it fires at the sub-agent's
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
n=$(cat "$SR_GUARDRAIL_DIR/../../../.onceonly.count" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$SR_GUARDRAIL_DIR/../../../.onceonly.count"
for path in $(` + pathsOfPayload + `); do
  echo "call $n path=[$path]" >> "$SR_GUARDRAIL_DIR/../../../.onceonly.log"
done
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
// On the retry the re-fired stop carries `stop_hook_active` and IS judged — a
// retry is not a pass — and this rule relents on its second look, so the cycle
// completes. The cap on how many times a retry may be refused is the project's
// stop_hook_block_cap (T015_08 / T015_08b).
func TestT015_07_ARefusedSubagentCycleRetriesAndThenFinishes(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	// The rule goes in its own commit, after the initial one: its range starts at
	// that commit's parent, so the project's own files are the base.
	e.FileGuard(proj, "onceonly", refusesOnceThenRelents, map[string]string{"record.sh": refuseOnceScript})
	e.CommitAll(proj, "the rule, before the session")

	sub := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "echo one > first.md"),
		Bash("sb2", "echo two > second.md"),
	).ThenCommit("the sub-agent's work"))

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
	blocking := e.SubagentBlockingErrors(proj, "s-015-07")
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
	for _, l := range e.FileGuardLedgerLines(proj, "onceonly", "../../../.onceonly.log") {
		if pathOf(l) == "first.md" || pathOf(l) == "second.md" {
			t.Fatalf("the DISPATCHING session's own cycle judged %q — a file that exists only in "+
				"the sub-agent's separate worktree. The delegated work was attributed to the "+
				"session that dispatched it. Line: %s", pathOf(l), l)
		}
	}

	// The sub-agent was sent round again: the harness says so in its own words.
	if e.SubagentStopFeedbackCount(proj, "s-015-07") < 1 {
		t.Fatalf("the sub-agent was never re-run after its cycle was refused. A Post refusal "+
			"cannot undo the write; the whole mechanism by which it gets anything corrected is "+
			"sending the agent round again:\n%s", res.Output)
	}

	// The guardrail ran a bounded number of times: it refused once and relented
	// on the judged retry, so the sub-agent did not loop.
	lines := subLedger(t, proj, theWorktree(t, proj), "onceonly", "../../../.onceonly.log")
	if len(lines) == 0 {
		t.Fatalf("the guardrail never ran at the sub-agent's cycle at all")
	}
	if len(lines) > 4 {
		t.Fatalf("the guardrail ran %d times (%v). It relents on its second look, so the "+
			"judged retry should have passed and ended the loop", len(lines), lines)
	}
}

// T015_08: under `stop_hook_block_cap: 1`, the same refusal re-fired judges
// nothing — the project's opt-in to the old one-refusal behaviour, observed from
// outside.
//
// Its own test because the property is not "the sub-agent finished" but "the
// second stop judged NOTHING". A rule that refuses EVERY time is the shape that
// separates them: the first cycle refuses, the stop re-fires with
// stop_hook_active set, the cap of 1 is reached, and subagent-stop lets it end
// rather than refuse a second time.
//
// The config is written before GitInit so the sub-agent's worktree carries it.
func TestT015_08_AReFiredSubagentStopJudgesNothingAgainUnderACapOfOne(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "always", refusesEverything, map[string]string{"record.sh": refuseAlwaysScript})
	writeBlockCap(t, proj, 1)
	e.GitInit(proj)

	sub := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "echo x > refused-work.md"),
	).ThenCommit("the sub-agent's work"))

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
	if len(e.SubagentBlockingErrors(proj, "s-015-08")) == 0 {
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
			"under stop_hook_block_cap: 1 is left alone; judging it again ignores the project's "+
			"cap", len(lines), lines)
	}
}

// T015_08b: a re-fired stop is still judged — an agent does not pass a rule by being sent
// round again. The always-refusing rule is a script: a stored script refusal is asked again at every
// re-fired stop (only a pass or a judge refusal is replayed), so the script runs each time, and the loop still ends: the engine's default cap (8, the
// harness's own) lets the turn end once it is reached, before the harness has to override.
func TestT015_08b_AReFiredSubagentStopAsksTheScriptAgain(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "always", refusesEverything, map[string]string{"record.sh": refuseAlwaysOutsideRules})
	e.GitInit(proj)

	sub := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "echo x > refused-work.md"),
	).ThenCommit("the sub-agent's work"))
	res := e.Run(proj, "s-015-08b", "delegate work that is always refused", Turns("root done",
		Dispatch("d1", "do the job", sub, "worktree"),
	))

	if !res.Saw("root done") {
		t.Fatalf("the dispatching session never completed — the refusal loop did not end:\n%s", res.Output)
	}
	if hitRetryCap(res.Output) {
		t.Fatalf("the harness had to override the hook; the engine's own cap should have ended the loop first:\n%s", res.Output)
	}
	// The refusal is the rule's own, and it was delivered at more than one stop (the sub-agent was
	// re-run each time: the de-duplicated record shows the text once, the stream shows the loop).
	if !strings.Contains(strings.Join(e.SubagentBlockingErrors(proj, "s-015-08b"), "\n"), "this rule always says no") {
		t.Fatalf("the rule's refusal never reached the sub-agent's own stop:\n%s", res.Output)
	}
	if n := e.SubagentStopFeedbackCount(proj, "s-015-08b"); n < 2 {
		t.Fatalf("the sub-agent was sent round %d time(s): the re-fired stop was not judged, so a rule "+
			"gave way to the sub-agent simply being sent round again", n)
	}
	if lines := readLines(t, filepath.Join(proj, ".claude", "worktrees", theWorktree(t, proj), ".refused.log")); len(lines) < 2 {
		t.Fatalf("the script ran %d times (%v): a script refusal is asked again at a re-fired stop", len(lines), lines)
	}
}

// writeBlockCap sets the project's stop_hook_block_cap in .sloprail/config.yaml.
func writeBlockCap(t *testing.T, proj string, n int) {
	t.Helper()
	cfg := filepath.Join(proj, ".sloprail", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf("stop_hook_block_cap: %d\n", n)), 0o644); err != nil {
		t.Fatal(err)
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

// refuseAlwaysOutsideRules is refuseAlwaysScript with its ledger at the root of the tree, outside
// the `.sloprail` whose files key every verdict (and which commit-required would ask to commit).
const refuseAlwaysOutsideRules = `#!/bin/sh
cat >/dev/null
echo "asked" >> "$SR_GUARDRAIL_DIR/../../../.refused.log"
echo '{"reason":"this rule always says no"}'
exit 1
`

// readLines is the non-empty lines of a file ("" lines dropped; none when it does not exist).
func readLines(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// T015_09: a sub-agent that changes nothing ends cleanly and judges nothing.
//
// The negative case that stops every other test in this package from being
// satisfied by an engine that fires guardrails indiscriminately. If a cycle with
// no difference still produced verdicts, "the sub-agent's file was judged" would
// carry no information — and a rule bound to created files would refuse a
// sub-agent that created none.
//
// A file-guard judges COMMITS, so a sub-agent that merely wrote something and never
// committed would be unjudged whatever the engine does with differences. Every case
// therefore commits, and the silence is about what the commits net to:
//
//   - one that commits nothing new (an empty commit);
//   - one that commits a file and then commits its removal — the tree ends where it
//     started, so a range measured START against END correctly sees nothing, while one
//     that tracked individual commits or tool calls would see a creation;
//   - the control, which commits a file it keeps: its cycle IS judged, so the silence
//     of the other two is an engine that looked and found nothing, not one that never
//     judges a sub-agent's commits.
func TestT015_09_ASubagentThatChangesNothingJudgesNothing(t *testing.T) {
	for _, tc := range []struct {
		name       string
		turns      []harness.Turn
		wantJudged bool
	}{
		{"commits nothing new", []harness.Turn{harness.Commit("sb1", "an empty commit")}, false},
		{"commits a file and then its removal", []harness.Turn{
			harness.CommitFile("sb1", "transient.md", "x\n", "add transient"),
			Bash("sb2", "rm transient.md"),
			harness.Commit("sb3", "remove transient"),
		}, false},
		{"control: commits a file it keeps", []harness.Turn{
			harness.CommitFile("sb1", "kept.md", "x\n", "add kept"),
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New(t)
			proj := e.Project()
			e.FileGuard(proj, "recorder", recordsPathAndSession, map[string]string{"record.sh": recordScript})
			e.GitInit(proj)

			sub := harness.SubagentScript(t, harness.Turns("sub done", tc.turns...))

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
			wtPath := filepath.Join(proj, ".claude", "worktrees", wt)

			// The premise: the sub-agent really did commit, in its own worktree.
			if e.Git(wtPath, "log", "--oneline", "-1", "--format=%s") == e.Git(proj, "log", "--oneline", "-1", "--format=%s") {
				t.Fatalf("the sub-agent's worktree holds no commit of its own, so no commit was made " +
					"and the silence below would be about an uncommitted cycle")
			}

			lines := subLedger(t, proj, wt, "recorder", "log")
			if tc.wantJudged {
				if len(lines) == 0 {
					t.Fatalf("the control did not get its committed file judged, so the silence in the " +
						"other cases proves nothing")
				}
				return
			}
			if len(lines) != 0 {
				t.Fatalf("a sub-agent whose commits leave the tree as they found it still had %d "+
					"verdict(s) recorded (%v). A cycle with no difference must judge nothing — "+
					"otherwise a rule bound to created files refuses a sub-agent that created "+
					"none, and every positive assertion in this package stops carrying "+
					"information", len(lines), lines)
			}

			// And the transient file really is gone, so the second case is the
			// case it claims to be.
			if strings.Contains(tc.name, "removal") {
				if _, err := os.Stat(filepath.Join(wtPath, "transient.md")); err == nil {
					t.Fatalf("the file the sub-agent deleted is still there, so the tree DID differ " +
						"and this case did not exercise what it says")
				}
				if e.Git(wtPath, "log", "--format=%s", "--", "transient.md") == "" {
					t.Fatalf("the transient file was never committed, so its removal is not a netted-out range")
				}
			}
		})
	}
}

// T015_10: a sub-agent that COMMITS its work still has its cycle judge it.
//
// The difference a cycle judges is the tree against the session's baseline, so
// what matters is WHERE the baseline is. It used to be taken at the sub-agent's
// first SubagentStop — after the commit, on the sub-agent's own commit — and a
// sub-agent that committed ended with an empty cycle: its guardrails never saw
// what it made. The engine now takes a sub-agent's point at its first tool
// call, which reaches PreToolUse before anything the sub-agent does can move
// HEAD, so committed work is inside the difference.
//
// This test used to pin the old answer ("nothing to judge") and said to
// re-derive it if that changed deliberately. It did: the first-tool baseline
// (#76) applies to a sub-agent as much as to a root, and the mock now sends a
// sub-agent's tool calls with transcript_path and agent_id as real Claude Code
// does, so the sub-agent's own store is found at that first call.
func TestT015_10_ASubagentThatCommitsStillHasItsWorkJudged(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "recorder", recordsPathAndSession, map[string]string{"record.sh": recordScript})
	e.GitInit(proj)

	sub := harness.SubagentScript(t, harness.Turns("sub done",
		harness.CommitFile("sb1", "committed-by-the-sub.md", "committed\n", "sub work"),
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
	// The guard's own ledger lives in the tree it guards and is written after the
	// commit; that one path is exempt, and anything else left uncommitted means
	// the sub-agent's work was never committed.
	for _, l := range strings.Split(strings.TrimSpace(e.Git(wtPath, "status", "--porcelain")), "\n") {
		if l != "" && l != "?? .sloprail/file-guard/recorder/log" {
			t.Fatalf("the sub-agent's worktree still has uncommitted changes (%q), so its work was "+
				"never committed and this test is not about a committed cycle", l)
		}
	}

	// The consequence: the committed file is judged by the sub-agent's cycle.
	lines := subLedger(t, proj, wt, "recorder", "log")
	judged := false
	for _, l := range lines {
		if strings.Contains(l, "committed-by-the-sub.md") {
			judged = true
		}
	}
	if !judged {
		t.Fatalf("a sub-agent that COMMITTED its work did not have it judged (ledger %v): its "+
			"baseline was taken after the commit, so the work is history rather than a difference", lines)
	}
}
