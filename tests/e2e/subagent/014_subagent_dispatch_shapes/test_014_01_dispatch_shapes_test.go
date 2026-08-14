package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The shapes a dispatch comes in, and what each one leaves behind.
//
// # What this harness can and cannot show about a sub-agent, MEASURED
//
// 013 states that a sub-agent's identity is not observable end to end through
// the plugin. That is right, and this package re-measured it rather than
// inheriting it — the conclusion is the same but the reason is sharper, and the
// sharper reason is what tells anyone extending this where the wall is.
//
// A probe bound a PreToolUse-driven guardrail and ran a dispatch whose
// sub-agent wrote a file, with the root writing one on either side. All three
// writes reached the guardrail, in order — so a sub-agent's tool calls DO reach
// a PreToolUse hook, which is more than 013 assumed. But every one of the three
// invocations was handed:
//
//   - the same SR_SESSION_ID (the ROOT's conversation identity),
//   - no agent_transcript_path and no agent_id on the payload at all, and
//   - one shared `session state` scope: the hook fired for the sub-agent's file
//     read back the value the root's previous hook had written, and the root's
//     next hook read back the value the SUB-AGENT's had.
//
// So under this harness a sub-agent's tool calls are dispatched AS THE PARENT.
// There is no sub-agent-scoped PreToolUse to observe, which means:
//
//	subagent_state_is_its_own   — not observable AT THIS EVENT. A sub-agent's
//	                              PRE hooks are the parent's under this harness.
//	                              Its POST cycle is its own, and that is where
//	                              T015_03, T015_04 and T015_06 observe it.
//	judged_on_its_own_record    — likewise: the Pre records are the parent's,
//	                              the Post cycle's is the sub-agent's own.
//	                              T015_01 and T015_02.
//	identity_comes_from_the_hook— unfalsifiable at the Pre event, since the hook
//	                              is told nothing about a sub-agent. At the Post
//	                              event the hook IS told, and what it is told is
//	                              what T015_01 asserts.
//
// The scope of this list is what changed, not the measurements under it. Every
// statement above about PreToolUse still holds and is still asserted by T014_01.
// What was wrong was reading "the Pre event cannot show this" as "an e2e cannot
// show this", when the sub-agent's own cycle is a second, differently-scoped
// event this package simply was not looking at.
//
// # The event that DOES arrive as a sub-agent, and why it still cannot be
// asserted on from here
//
// SubagentStop does fire in the sub-agent, and it arrives correctly routed —
// measured, not assumed. A probe writing from inside `sr-session
// subagent-stop` recorded, for each dispatch:
//
//	agent_id              a real per-sub-agent id
//	agent_transcript_path .../<session>/subagents/agent-<id>.jsonl
//	IsSubagent()          true
//
// So the engine's routing IS reached end to end by this harness: record()
// prefers the sub-agent's own path, and stableID resolves the sub-agent's own
// identity from it, on every dispatch these tests make.
//
// What travels OUT of the hook, corrected. This section previously recorded
// that a SubagentStop hook's exit status has "NO observable consequence", and
// that a test asserting a delegated cycle completes cleanly "passes just as well
// against a subagent-stop that hard-blocks every cycle". That is wrong, and the
// correction matters because the claim was being used to justify not testing a
// sub-agent's cycle at all.
//
// Re-running the same mutation says the opposite. With `sr-session
// subagent-stop` mutated to refuse every cycle — tried BOTH ways, exit 2 and an
// exit-0 {"decision":"block"} frame — T014_06, T014_07 and T014_08 all fail, and
// the mock's stderr carries the reason verbatim:
//
//	claude-mock: SubagentStop blocked (hooks: command blocked: …) — re-running subagent (turn 1)
//	… turns 2 through 8 …
//	claude-mock: SubagentStop still blocked after 8 turns (cap) — giving up
//
// So the retry loop IS driven and the cap IS reached, both channels work, and
// the block's own text reaches the captured output. The original measurement was
// most likely taken against a subagent-stop that could not reach its refusal —
// at the time it was a stub returning nil before doing anything.
//
// Three limits are real and remain, and the FIRST is the one that decides what
// this package can test about a sub-agent's own cycle.
//
//   - An isolated sub-agent's WRITE calls are never applied. Measured by
//     dispatching a sub-agent whose scenario writes from-sub.md and then reading
//     the bound worktree, which is empty of it.
//
//     THE CONCLUSION DRAWN FROM THAT WAS WRONG, and it is corrected in
//     015_subagent_own_cycle rather than here. This bullet used to continue: "So
//     a worktree-isolated sub-agent produces no tree difference, its Post cycle
//     has nothing to judge, and no guardrail can fire in it however correct the
//     engine is. A test asserting 'the sub-agent's own file was judged' is
//     therefore unwritable here." The generalisation from Write to all tool
//     calls is the error. The mock DOES execute Bash, and for an isolated
//     sub-agent it executes it inside the bound worktree — so there is a real
//     tree difference, the worktree carries its own checkout of
//     .sloprail/guardrails, and the rule fires under the sub-agent's OWN
//     identity. The test called unwritable is T015_01, and the two invariants
//     called unobservable below are covered by T015_01 through T015_06.
//
//     `subagent-stop` taking the baseline and reaching dispatch in the
//     sub-agent's own store is pinned at unit level as well, in
//     services/sr-session/session_subagent_stop_test.go.
//   - A hook that exits 0 without blocking is silent: neither stream is
//     forwarded, so a PASSING cycle cannot be observed from here. Any assertion
//     has to be shaped around a refusal.
//   - The block reaches the SUB-AGENT, not the parent. The sub-agent's turn is
//     re-run with the feedback, which is the right shape; but once its retries
//     are exhausted the tool result reports success regardless, so the
//     dispatching session is never told a guardrail refused delegated work.
//
// # What would close the remaining gap
//
// Named rather than worked around:
//
//  1. For the three invariants above: the harness would have to report a
//     sub-agent's own transcript on the payloads of the TOOL CALLS it makes —
//     agent_transcript_path or agent_id, the fields record() already prefers —
//     which is what real Claude Code does and what every unit test in
//     services/sr-session/subagent_test.go drives directly.
//  2. For a sub-agent's own CYCLE to be assertable: the mock would have to apply
//     an isolated sub-agent's tool calls inside the worktree it bound, so that
//     there is a difference for its Post cycle to judge.
//  3. For a sub-agent's verdict to reach the session that dispatched it: the
//     Agent tool result would have to carry it. Today a refused sub-agent and a
//     clean one are indistinguishable to the parent.
//
// Hand-wiring a lifecycle hook to fake any of them would be arranging wiring no
// user has, and whatever it then proved would be about the arrangement.
//
// # What IS observable, and is therefore what this package tests
//
// The dispatch shape, which is real and which mutating DOES break: how many
// sub-agents ran, whether each got the tree it asked for, and whether the
// dispatching session's own guardrails still fire on both sides of a
// delegation. Every assertion below is one of those.

const watcherDecl = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./watch.sh
  PreFileUpdate:
    - hooks:
        - type: command
          command: ./watch.sh
---

# Records every file event it is asked about, and permits it.
`

// It records the SESSION IDENTITY it was handed and whether the payload named
// an agent, not only the path.
//
// The path alone cannot express the measurement this package's coverage rests
// on. A harness that scoped sub-agents properly would still dispatch the
// sub-agent's write to a guardrail, and the ledger line would be
// byte-identical — so an assertion on the path is green under both the measured
// arrangement and its opposite, which is the one thing it must not be. What
// separates them is WHOSE session the call was judged as, and that travels in
// SR_SESSION_ID and in the payload's agent fields.
const watcherScript = `#!/bin/sh
payload=$(cat)
path=$(printf '%s' "$payload" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')
agent=none
case "$payload" in
  *'"agent_id"'*|*'"agent_transcript_path"'*) agent=present ;;
esac
echo "asked path=[$path] session=[$SR_SESSION_ID] agent=[$agent]" >> "$PWD/log"
exit 0
`

// T014_01: a dispatch WITHOUT isolation leaves the root's guardrails live on
// both sides of it.
//
// The ordinary path, and the one that would fail silently. A dispatch is a tool
// call like any other; if handling it disturbed the session — an identity that
// changed under the root, a store reopened somewhere else, a hook process left
// in a bad state — the damage would show up not in the sub-agent but in the
// ROOT's next write, which is the thing nobody thinks to check.
//
// So the root writes on either side of the dispatch and both writes must be
// judged. And the sub-agent's own write reaches the guardrail too, which is
// what this harness does with a shared tree: its tool calls are dispatched as
// the parent's (see the note above). That is asserted as the measured fact it
// is, so that a harness change making sub-agents genuinely separate FAILS here
// and is noticed, rather than silently altering what every test in this tree
// means.
func TestT014_01_ASharedTreeDispatchLeavesTheRootsGuardrailsLive(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "watcher", watcherDecl, map[string]string{"watch.sh": watcherScript})

	sub := writeScenario(t, proj, "sub.sh", harness.Turns("sub done",
		Write("s1", "from-sub.md", "the sub-agent's own work"),
	))

	res := e.Run(proj, "s-014-01", "write, delegate, write", Turns("root done",
		Write("r0", "before.md", "root before"),
		Dispatch("d1", "do the job", sub, ""),
		Write("r1", "after.md", "root after"),
	))

	if !res.Saw("root done") {
		t.Fatalf("the session did not complete around a shared-tree dispatch:\n%s", res.Output)
	}

	// The tree really was shared — no worktree was bound. Otherwise this is the
	// isolated case under another name and the shared-tree claim rests on
	// nothing.
	if trees := worktrees(t, proj); len(trees) != 0 {
		t.Fatalf("a worktree was bound (%v) for a dispatch that asked for none", trees)
	}

	// The root's writes on BOTH sides of the dispatch were judged. A dispatch
	// that disarmed the session would show up here rather than in the
	// sub-agent.
	lines := e.Ledger(proj, "watcher", "log")
	for _, want := range []string{"path=[before.md]", "path=[after.md]"} {
		if !containsLine(lines, want) {
			t.Fatalf("the root's write %s was not judged — a dispatch must not disturb the "+
				"session it was made from, and the damage from one that does lands on the "+
				"ROOT's next write rather than on the sub-agent. Ledger: %v", want, lines)
		}
	}

	// And the measured fact about this harness: with a shared tree, the
	// sub-agent's own tool call reaches the guardrail as the parent's.
	//
	// Asserted so a harness that starts scoping sub-agents properly fails here
	// LOUDLY. This file's opening note reasons from this measurement, and three
	// invariants are declared unreachable because of it — so it must not be
	// allowed to change quietly underneath that reasoning.
	if !containsLine(lines, "path=[from-sub.md]") {
		t.Fatalf("the sub-agent's own write did not reach the guardrail. This harness dispatches "+
			"a shared-tree sub-agent's tool calls as the PARENT's, which is the measurement this "+
			"package's coverage claims rest on — see the note at the top of this file. If that "+
			"has changed, those claims need re-deriving rather than patching. Ledger: %v", lines)
	}

	// And the measurement itself, which the path alone cannot express.
	//
	// That the sub-agent's write REACHED the guardrail is true under both
	// arrangements: a harness scoping sub-agents properly would dispatch it too,
	// and the ledger line would carry the same path. So the assertion above
	// cannot fail on the condition its own message claims to detect. What
	// separates the two is whose session the call was judged as — and under the
	// measured arrangement all three calls, the sub-agent's included, are handed
	// the ROOT's identity and no agent fields at all.
	//
	// Asserted so that a harness which starts presenting sub-agent tool calls as
	// the sub-agent's own fails HERE. Three invariants are declared unreachable
	// on the strength of this measurement; it must not be allowed to change
	// underneath that reasoning while every test stays green.
	var root string
	for _, l := range lines {
		if strings.Contains(l, "path=[before.md]") {
			root = sessionOf(l)
		}
	}
	if root == "" {
		t.Fatalf("the root's own write recorded no session id, so there is nothing to compare "+
			"the sub-agent's against and the measurement below would pass vacuously. Ledger: %v", lines)
	}
	for _, l := range lines {
		if got := sessionOf(l); got != root {
			t.Fatalf("a call was judged as session %q while the root's own write was judged as %q. "+
				"This harness dispatches a shared-tree sub-agent's tool calls as the PARENT's, and "+
				"three invariants in this package are declared unobservable BECAUSE of that. If "+
				"sub-agents are now scoped separately, those claims need re-deriving rather than "+
				"patching. Ledger: %v", got, root, lines)
		}
		if !strings.Contains(l, "agent=[none]") {
			t.Fatalf("a PreToolUse payload named an agent (%s). The measurement this package rests "+
				"on is that it names none — record() prefers agent_transcript_path/agent_id, so a "+
				"harness supplying them makes sub-agent tool calls separately scoped and the "+
				"unobservability claims above stop holding. Ledger: %v", l, lines)
		}
	}
}

// sessionOf reads the session identity a ledger line recorded, or "" when the
// line carries none.
//
// The identity is what distinguishes a sub-agent's call judged as its own from
// one judged as the parent's, and it is the only thing on the line that does.
func sessionOf(line string) string {
	const marker = "session=["
	i := strings.Index(line, marker)
	if i < 0 {
		return ""
	}
	rest := line[i+len(marker):]
	j := strings.IndexByte(rest, ']')
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// T014_02: a dispatch WITH worktree isolation binds a real separate tree, and
// still leaves the root's guardrails live.
//
// The isolated case is the one the design turns on: a sub-agent judging
// different content at the same repository-relative paths is where confusing it
// with its parent does real damage. What is checkable here is that the
// isolation is REAL rather than merely requested — the mock binds a genuine
// `git worktree add` — and that the root is undisturbed by it.
//
// Paired with T014_01 deliberately. The two differ in ONE dispatch field, which
// is the point: sharing the tree is meant to be the ordinary path rather than a
// second mechanism. A change that made one work and the other hang would be
// caught by having both.
func TestT014_02_AnIsolatedDispatchBindsARealTreeAndLeavesTheRootLive(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "watcher", watcherDecl, map[string]string{"watch.sh": watcherScript})

	sub := writeScenario(t, proj, "sub.sh", harness.Turns("sub done",
		Write("s1", "from-sub.md", "the sub-agent's own work"),
	))

	res := e.Run(proj, "s-014-02", "write, delegate in isolation, write", Turns("root done",
		Write("r0", "before.md", "root before"),
		Dispatch("d1", "do the job", sub, "worktree"),
		Write("r1", "after.md", "root after"),
	))

	if !res.Saw("root done") {
		t.Fatalf("the session did not complete around an isolated dispatch — a SubagentStop hook "+
			"that exits non-zero blocks and re-runs the turn, so a failure here is delegation "+
			"becoming impossible rather than merely unguarded:\n%s", res.Output)
	}

	// Exactly one worktree, genuinely on disk. Checked by the tree rather than
	// by where the sub-agent's file landed, because the mock does not APPLY an
	// isolated sub-agent's tool calls — an assertion on from-sub.md would hold
	// for a sub-agent that was never isolated, or never dispatched.
	trees := worktrees(t, proj)
	if len(trees) != 1 {
		t.Fatalf("want one worktree bound for the isolated sub-agent, found %d (%v) — "+
			"isolation=%q did not bind a separate tree, so the isolated case was never "+
			"exercised and this test is T014_01 under another name", len(trees), trees, "worktree")
	}

	// The dispatching session's own repository survives, which is what makes
	// the worktree a SEPARATE tree rather than a moved one.
	if _, err := os.Stat(filepath.Join(proj, ".git")); err != nil {
		t.Fatalf("the dispatching session's own repository is gone: %v", err)
	}

	// The root's guardrails still fired on both sides.
	lines := e.Ledger(proj, "watcher", "log")
	for _, want := range []string{"path=[before.md]", "path=[after.md]"} {
		if !containsLine(lines, want) {
			t.Fatalf("the root's write %s was not judged around an isolated dispatch. Ledger: %v",
				want, lines)
		}
	}
}

// T014_03: two sub-agents in one session, each isolated, are kept apart by the
// harness — and the root survives both.
//
// A session with two sub-agents has three sessions in play, and it is the case
// where pooling does the most damage: the spec's own example is a session
// spawning ten sub-agents with all ten reading each other's notes as their own.
//
// What is observable here is the PRECONDITION for the engine's per-session
// keying meaning anything: that the harness gives the two distinct agent ids and
// distinct trees at all. If it collapsed them into one, nothing downstream could
// keep them apart however correct it was, and every test about doing so would be
// untestable rather than passing.
//
// That the two resolve to distinct IDENTITIES is
// internal/transcript.TestTwoSubagentsOfOneParentAreDistinct, for the reasons in
// this file's opening note.
func TestT014_03_TwoIsolatedSubagentsGetDistinctIdsAndTrees(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "watcher", watcherDecl, map[string]string{"watch.sh": watcherScript})

	first := writeScenario(t, proj, "sub1.sh", harness.Turns("one done",
		Write("s1", "one.md", "first sub-agent")))
	second := writeScenario(t, proj, "sub2.sh", harness.Turns("two done",
		Write("s2", "two.md", "second sub-agent")))

	res := e.Run(proj, "s-014-03", "delegate twice", Turns("root done",
		Dispatch("d1", "first job", first, "worktree"),
		Dispatch("d2", "second job", second, "worktree"),
		Write("r1", "after-both.md", "root after both"),
	))

	if !res.Saw("root done") {
		t.Fatalf("a session dispatching two sub-agents did not complete:\n%s", res.Output)
	}

	// Two dispatches, two distinct agent ids. Equal ids would mean the harness
	// ran one sub-agent, under which nothing about keeping two apart could be
	// exercised here at all.
	ids := agentIDs(res.Output)
	if len(ids) != 2 {
		t.Fatalf("want two dispatched sub-agents, saw %d (%v):\n%s", len(ids), ids, res.Output)
	}
	if ids[0] == ids[1] {
		t.Fatalf("both dispatches reported the agent id %s — the harness ran one sub-agent, not "+
			"two, so two sessions were never in play", ids[0])
	}

	// A tree each, rather than one reused. A single worktree would mean two
	// sub-agents' work was one tree's diff.
	if trees := worktrees(t, proj); len(trees) != 2 {
		t.Fatalf("want a worktree per sub-agent, found %d (%v)", len(trees), trees)
	}

	// The root is still live after both.
	if !containsLine(e.Ledger(proj, "watcher", "log"), "path=[after-both.md]") {
		t.Fatalf("the root's write after two dispatches was not judged — two delegations must "+
			"leave the dispatching session working. Ledger: %v", e.Ledger(proj, "watcher", "log"))
	}
}

// T014_04: two sub-agents in one session with the tree SHARED.
//
// The same multiplicity without isolation, which is the arrangement where
// pooling is most tempting and most wrong: both sub-agents and the root are
// looking at one directory, so the only thing that could keep their state apart
// is the session keying itself.
//
// Under this harness their tool calls all arrive as the parent's (see the
// opening note), so what is provable here is that the session survives two
// shared-tree delegations with distinct ids and no worktrees — the shape, not
// the scoping. Stated as such rather than dressed up.
func TestT014_04_TwoSharedTreeSubagentsStillLeaveTheSessionWorking(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "watcher", watcherDecl, map[string]string{"watch.sh": watcherScript})

	first := writeScenario(t, proj, "sub1.sh", harness.Turns("one done",
		Write("s1", "one.md", "first sub-agent")))
	second := writeScenario(t, proj, "sub2.sh", harness.Turns("two done",
		Write("s2", "two.md", "second sub-agent")))

	res := e.Run(proj, "s-014-04", "delegate twice in the same tree", Turns("root done",
		Dispatch("d1", "first job", first, ""),
		Dispatch("d2", "second job", second, ""),
		Write("r1", "after-both.md", "root after both"),
	))

	if !res.Saw("root done") {
		t.Fatalf("a session dispatching two shared-tree sub-agents did not complete:\n%s",
			res.Output)
	}
	ids := agentIDs(res.Output)
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("want two distinct sub-agents, saw %v:\n%s", ids, res.Output)
	}
	if trees := worktrees(t, proj); len(trees) != 0 {
		t.Fatalf("a worktree was bound (%v) for dispatches that asked for none", trees)
	}
	if !containsLine(e.Ledger(proj, "watcher", "log"), "path=[after-both.md]") {
		t.Fatalf("the root's write after two shared-tree dispatches was not judged. Ledger: %v",
			e.Ledger(proj, "watcher", "log"))
	}
}

// T014_05: the two isolation shapes MIXED in one session.
//
// A session does not commit to one kind of delegation, and the mixed case is
// the one nobody writes by hand. If handling an isolated dispatch left anything
// set — a cached tree, a resolved identity, a bound worktree the next dispatch
// inherits — a shared-tree dispatch that FOLLOWED one would be silently
// isolated, or the reverse.
//
// So: isolated, then shared, then isolated. Exactly two worktrees must exist
// afterwards. Three would mean the shared dispatch bound one it never asked for;
// one would mean the second isolated dispatch reused the first's tree.
func TestT014_05_MixedIsolationInOneSessionBindsExactlyTheTreesAskedFor(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "watcher", watcherDecl, map[string]string{"watch.sh": watcherScript})

	isolatedOne := writeScenario(t, proj, "iso1.sh", harness.Turns("iso one done",
		Write("s1", "iso1.md", "isolated one")))
	shared := writeScenario(t, proj, "shared.sh", harness.Turns("shared done",
		Write("s2", "shared.md", "shared")))
	isolatedTwo := writeScenario(t, proj, "iso2.sh", harness.Turns("iso two done",
		Write("s3", "iso2.md", "isolated two")))

	res := e.Run(proj, "s-014-05", "mix the dispatch shapes", Turns("root done",
		Dispatch("d1", "isolated", isolatedOne, "worktree"),
		Dispatch("d2", "shared", shared, ""),
		Dispatch("d3", "isolated again", isolatedTwo, "worktree"),
		Write("r1", "after-all.md", "root after all three"),
	))

	if !res.Saw("root done") {
		t.Fatalf("a session mixing isolated and shared dispatches did not complete:\n%s",
			res.Output)
	}

	if ids := agentIDs(res.Output); len(ids) != 3 {
		t.Fatalf("want three dispatched sub-agents, saw %d (%v):\n%s", len(ids), ids, res.Output)
	}

	// Two worktrees: one per dispatch that asked, none for the one that did
	// not. This is the assertion that catches isolation leaking between
	// dispatches in either direction.
	trees := worktrees(t, proj)
	if len(trees) != 2 {
		t.Fatalf("want exactly two worktrees — one for each dispatch that asked for isolation, "+
			"and none for the shared one — found %d (%v). Three means the shared dispatch was "+
			"isolated anyway; one means the second isolated dispatch reused the first's tree. "+
			"Either way the isolation of one dispatch is leaking into the next", len(trees), trees)
	}

	if !containsLine(e.Ledger(proj, "watcher", "log"), "path=[after-all.md]") {
		t.Fatalf("the root's write after three mixed dispatches was not judged. Ledger: %v",
			e.Ledger(proj, "watcher", "log"))
	}
}

// T014_06: a sub-agent whose own cycle is REFUSED does not trap it, and does
// not take the root down with it.
//
// The failure this rules out is the worst one in the feature, and it is a
// deadlock rather than a wrong answer. SubagentStop treats a non-zero exit as a
// block and re-runs the sub-agent's turn; a rule that refuses a sub-agent's
// cycle on a condition the retry cannot change turns "this work is refused" into
// "this sub-agent can never finish". The harness caps the retries and reports
// "SubagentStop still blocked after N", which is what a hang looks like from
// outside.
//
// The engine's answer is that subagent-stop declines rather than blocks when it
// cannot place a cycle — pinned at unit level by
// TestSubagentStopNeverBlocksOnAnUnplaceableCycle. What is added here is the
// same claim through a user's own wiring, with a rule live that refuses
// everything it is asked about: the dispatch must still terminate.
//
// The refusing rule is bound to the FILE kinds rather than to the cycle's end,
// because a guardrail cannot bind SubagentStop — the plugin owns that binding,
// and a test hand-wiring one would be arranging wiring no user has. So what is
// exercised is a session in which refusals really are travelling while a
// delegation happens, which is the realistic version: an agent delegating work
// in a session where a rule is actively refusing.
func TestT014_06_ARefusingSessionStillCompletesItsDelegations(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "refuser", watcherDecl, map[string]string{"watch.sh": refusingScript})

	sub := writeScenario(t, proj, "sub.sh", harness.Turns("sub done",
		Write("s1", "from-sub.md", "delegated work"),
	))

	res := e.Run(proj, "s-014-06", "refuse and delegate", Turns("root done",
		Write("r0", "refused.md", "this will be refused"),
		Dispatch("d1", "delegate anyway", sub, "worktree"),
		Write("r1", "also-refused.md", "so will this"),
	))

	// The session finished. A sub-agent trapped in the SubagentStop retry loop
	// would not get here, and the harness would say so.
	if !res.Saw("root done") {
		t.Fatalf("the session did not complete with a refusing rule live across a delegation — a "+
			"sub-agent that cannot finish is worse than one that is unguarded, because it is a "+
			"deadlock rather than a gap:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "still blocked after") {
		t.Fatalf("a sub-agent hit the harness's retry cap, which is what a trapped delegated "+
			"cycle looks like from outside:\n%s", res.Output)
	}

	// The refusals really were travelling, or "it completed" is the reading of
	// a session in which nothing was being refused at all and the test proves
	// nothing about refusing across a delegation.
	if n := strings.Count(res.Output, "this rule says no"); n < 2 {
		t.Fatalf("the rule refused %d time(s), want at least 2 (the root's writes on either side "+
			"of the dispatch) — without refusals in flight, completing the delegation says "+
			"nothing:\n%s", n, res.Output)
	}

	// And the isolated dispatch still bound its tree while all that was going
	// on: a refusal in the session must not quietly downgrade the delegation.
	if trees := worktrees(t, proj); len(trees) != 1 {
		t.Fatalf("want one worktree for the isolated dispatch, found %d (%v) — a session with "+
			"refusals in flight must still delegate the way it asked to", len(trees), trees)
	}
}

// T014_07: a dispatch nested inside a sub-agent's own scenario does not break
// the outer cycle.
//
// What this pins is that an Agent tool call appearing INSIDE a delegated
// scenario does not hang or fail the outer dispatch. That is a real shape — a
// sub-agent asked to delegate further is ordinary in production — and the
// failure mode is a delegated cycle that never ends.
//
// It does NOT establish anything about nesting, and it used to claim the
// opposite. The claim was: "MEASURED: this harness does not recursively execute
// a sub-agent's own Agent call — a probe whose sub-agent dispatched a third
// scenario reported exactly one agentId, the outer's, and the inner scenario
// never ran."
//
// The observation was real; the inference was not. agentIDs reads the OUTER
// run's stream, and a nested dispatch is announced in the SUB-AGENT'S stream,
// which the outer run never carries — so the count is 1 whether nesting happens
// or not, and could never have distinguished the two. Looking where the deeper
// sub-agent's WORK would land settles it: it runs, in the delegating
// sub-agent's tree, and is judged at a cycle of its own under a third identity.
// T015_12 asserts exactly that.
//
// The count below is kept, re-labelled as what it actually measures: how many
// dispatches THIS session announced.
func TestT014_07_ADispatchInsideASubagentsScenarioDoesNotBreakTheOuterCycle(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "watcher", watcherDecl, map[string]string{"watch.sh": watcherScript})

	innermost := writeScenario(t, proj, "inner.sh", harness.Turns("inner done",
		Write("i1", "inner.md", "innermost work")))
	outer := writeScenario(t, proj, "outer.sh", harness.Turns("outer done",
		Dispatch("od1", "delegate further", innermost, ""),
		Write("o1", "outer.md", "outer work")))

	res := e.Run(proj, "s-014-07", "delegate to a delegator", Turns("root done",
		Dispatch("d1", "outer job", outer, ""),
		Write("r1", "after.md", "root after"),
	))

	if !res.Saw("root done") {
		t.Fatalf("the session did not complete when the sub-agent's own scenario contained a "+
			"dispatch — a delegated cycle that never ends is a deadlock, and a sub-agent asked "+
			"to delegate further is an ordinary thing to ask:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "still blocked after") {
		t.Fatalf("a cycle hit the harness's retry cap:\n%s", res.Output)
	}

	// THIS session announced exactly one dispatch — its own. The nested one is
	// announced in the sub-agent's stream, which this run does not carry, so
	// this number says nothing about whether nesting happened. It is asserted
	// only to catch the outer dispatch silently not happening, or happening
	// twice.
	if ids := agentIDs(res.Output); len(ids) != 1 {
		t.Fatalf("the dispatching session announced %d dispatch(es) (%v), want its own one. This "+
			"count is about THIS session's stream only — a nested dispatch is announced in the "+
			"sub-agent's own stream. For what nesting actually does, see T015_12:\n%s",
			len(ids), ids, res.Output)
	}

	// The root is still live afterwards.
	if !containsLine(e.Ledger(proj, "watcher", "log"), "path=[after.md]") {
		t.Fatalf("the root's write after a nested-dispatch delegation was not judged. Ledger: %v",
			e.Ledger(proj, "watcher", "log"))
	}
}

// T014_08: a dispatch in a project that is NOT a git repository still
// completes.
//
// isolation="worktree" asks for a `git worktree add`, and in a project with no
// repository there is nothing to add one from. That is not exotic: a scratch
// directory, a fresh project, a tree whose .git is elsewhere. The engine's own
// posture on this is established elsewhere — a cycle whose difference cannot be
// established is not a rule being violated, and refusing over the engine's
// inability to look would be a refusal no guardrail asked for.
//
// What must not happen is the delegation failing. A sub-agent that cannot be
// isolated should fall back or proceed, never hang: the retry loop is the cost
// of getting this wrong, and it is silent.
func TestT014_08_ADispatchInANonRepositoryStillCompletes(t *testing.T) {
	e := New(t)
	proj := e.Project() // deliberately NOT GitInit
	e.Guardrail(proj, "watcher", watcherDecl, map[string]string{"watch.sh": watcherScript})

	sub := writeScenario(t, proj, "sub.sh", harness.Turns("sub done",
		Write("s1", "from-sub.md", "delegated work")))

	res := e.Run(proj, "s-014-08", "delegate without a repository", Turns("root done",
		Dispatch("d1", "job", sub, "worktree"),
		Write("r1", "after.md", "root after"),
	))

	if !res.Saw("root done") {
		t.Fatalf("a delegation in a project with no git repository did not complete. A sub-agent "+
			"that cannot be isolated must proceed or fall back, never hang — and a project "+
			"without a repository is an ordinary thing, not an edge case:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "still blocked after") {
		t.Fatalf("a cycle hit the harness's retry cap in a non-repository project:\n%s", res.Output)
	}

	// The root's guardrails are still live, which is the thing a failed
	// isolation could plausibly have taken with it.
	if !containsLine(e.Ledger(proj, "watcher", "log"), "path=[after.md]") {
		t.Fatalf("the root's write after a dispatch in a non-repository was not judged. "+
			"Ledger: %v", e.Ledger(proj, "watcher", "log"))
	}
}

// refusingScript records that it was asked and then refuses.
const refusingScript = `#!/bin/sh
payload=$(cat)
path=$(printf '%s' "$payload" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')
echo "asked path=[$path]" >> "$PWD/log"
echo "this rule says no" >&2
exit 2
`

// containsLine reports whether any ledger line contains the substring.
func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

// worktrees lists the worktree directories the mock bound for this project's
// sub-agents. An absent directory means none were bound.
func worktrees(t *testing.T, proj string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(proj, ".claude", "worktrees"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read worktrees: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// agentIDs returns the agent ids the mock announced, in order.
//
// The mock reports "agentId: <id>" in each dispatch's tool result — the
// harness's own statement about how many sub-agents it ran and which they were.
// Read from the stream rather than counted from the scenario, so a dispatch that
// silently did not happen shows up as a missing id.
//
// The label travels inside a JSON string, so the newline after the id is the two
// characters \ and n rather than a real one; both it and a literal newline end
// the id.
func agentIDs(output string) []string {
	const label = "agentId: "
	var ids []string
	for rest := output; ; {
		i := strings.Index(rest, label)
		if i < 0 {
			return ids
		}
		rest = rest[i+len(label):]
		id := rest
		if end := strings.IndexAny(id, " \"\n"); end >= 0 {
			id = id[:end]
		}
		if end := strings.Index(id, `\n`); end >= 0 {
			id = id[:end]
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
}

// writeScenario renders a scenario to a script the mock runs as a sub-agent,
// and returns its path.
func writeScenario(t *testing.T, proj, name string, s harness.Scenario) string {
	t.Helper()
	path := filepath.Join(proj, name)
	if err := s.Script(path); err != nil {
		t.Fatalf("write scenario %s: %v", name, err)
	}
	return path
}
