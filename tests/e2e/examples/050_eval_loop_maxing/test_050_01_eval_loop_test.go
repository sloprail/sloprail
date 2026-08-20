package e2e

import (
	"testing"
)

// This file drives the SHIPPED eval-loop-maxing example end to end — the whole
// .sloprail tree installed verbatim, exactly what a user lifts. The engine
// mechanics of the goal-tracking + goal-verify composite are already pinned by
// tests/e2e/context/035_context_dispatch (T035_07) with INLINE guardrails; what
// these tests add is that the SHIPPED scripts (context/goal-tracking/enter.sh,
// gate/goal-verify/run-verify.sh, exit.sh) actually drive the loop:
//
//   - a goal-tracking CONTEXT enters on a settled PostFileWrite of
//     goal/<name>/goal.yaml, activates for an enabled goal, and carries the
//     goal's name in its payload;
//   - a goal-verify GATE bound to Stop, require:[{context: goal-tracking}], reads
//     that payload and runs the goal's own verify.sh — BLOCKING the Stop with
//     "keep iterating until verify.sh passes" while the target is unmet;
//   - the context's exit reads the gate's verdict from `gates` and deactivates
//     once the gate passed.
//
// The goal itself is NOT installed from the example: README is explicit that
// goal/<name>/{goal.yaml,verify.sh} is a project-level sibling of .sloprail/,
// authored BY THE AGENT mid-trajectory (before that write the folder does not
// exist). So the tests write goal.yaml through a real agent turn and lay down a
// verify.sh whose verdict the test controls by flipping a single file.

const goalName = "accuracy-target"

// goalYAML is what an agent writes the moment it commits to a target: enabled,
// pointing at verify.sh as the goal's condition. Exactly the shape the shipped
// enter.sh reads (`^enabled:` must be true) and run-verify.sh resolves
// (`^script:` names the script it runs).
const goalYAML = "enabled: true\nscript: verify.sh\n"

// goalVerify is the goal's fixed condition, laid into goal/<name>/verify.sh as an
// executable. It passes only once the target-met marker exists in the workspace —
// the deterministic stand-in for "the measured metric now meets the threshold",
// flipped by the agent writing that marker. It reads $SR_WORKSPACE the same way
// the shipped verify.sh does, so it resolves the marker from the project root
// regardless of the check's cwd.
const goalVerify = `#!/bin/sh
if [ -f "${SR_WORKSPACE:-.}/target-met" ]; then
  exit 0
fi
echo "accuracy below target" >&2
exit 1
`

// keepIterating is the sentence the SHIPPED run-verify.sh refuses a premature
// Stop with. Matched on rather than on the word "block", so the test cannot pass
// on a refusal that came from anywhere else in the engine.
const keepIterating = "keep iterating until verify.sh passes"

// T050_01: the shipped example drives the loop — an unmet goal BLOCKS the Stop
// and RE-FIRES on the next Stop still unmet, then a met goal ADMITS.
//
// One session, three cycles, because the loop is a property of the session's
// accumulated state:
//
//   - Cycle 1 (violation): the agent authors goal/accuracy-target/goal.yaml
//     (enabled) with the marker absent. The context enters and activates carrying
//     the goal; the goal-verify gate reads it, runs verify.sh, it fails, and the
//     Stop is blocked with "keep iterating". GateState is fail.
//   - Cycle 2 (re-fire): the agent does more work but STILL does not meet the
//     target. The same gate wakes on this Stop, its require is still met (context
//     still active), verify.sh still fails, and the Stop is blocked AGAIN — proof
//     the gate re-fires rather than blocking only the first time. GateState is
//     still fail.
//   - Cycle 3 (fix): the agent writes the target-met marker. verify.sh now
//     passes, the gate admits the Stop, GateState flips to pass, and the context's
//     exit reads that pass and deactivates.
func TestT050_01_ShippedExampleDrivesTheLoop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj) // a Stop dispatch establishes a baseline; give it a repo
	installExampleTree(t, proj)

	// The goal's verify.sh is laid down before the run (the agent authors goal.yaml
	// during the trajectory; verify.sh is the goal's fixed condition it points at).
	e.WriteExecutable(proj, "goal/"+goalName+"/verify.sh", goalVerify)

	sess := "s-050-01"

	// ---- Cycle 1: declare the goal (enabled) with the target unmet. ----
	res1 := e.Run(proj, sess, "commit to the accuracy target", Turns("done",
		Write("g1", "goal/"+goalName+"/goal.yaml", goalYAML),
	))

	// The shipped context entered on the settled goal.yaml write and activated,
	// carrying the goal's name in its payload — the tracking half of the loop.
	active, payload := e.ContextState(proj, sess, "goal-tracking")
	if !active {
		t.Fatalf("the shipped goal-tracking context did not activate on the enabled goal.yaml write:\n%s", res1.Output)
	}
	if payload["goal"] != goalName {
		t.Fatalf("the context payload did not carry the goal name, got %v — the gate reads .payload.goal to find which verify.sh to run", payload)
	}

	// The shipped gate read the goal via its require and refused the Stop while the
	// target was unmet, and its reason reached the agent.
	blocks1 := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks1) == 0 {
		t.Fatalf("the shipped goal-verify gate did not block the Stop while the target was unmet")
	}
	if !containsAny(blocks1, keepIterating) {
		t.Fatalf("the gate's %q reason did not reach the agent:\n%v", keepIterating, blocks1)
	}
	if status := e.GateState(proj, sess, "goal-verify"); status != "fail" {
		t.Fatalf("the goal-verify gate recorded %q after an unmet target, want fail", status)
	}
	// The raw count of "keep iterating" refusals on the record after cycle 1 — the
	// baseline the re-fire in cycle 2 is measured against.
	blocksAfter1 := rawBlockRecords(t, e, proj, sess, keepIterating)
	if blocksAfter1 == 0 {
		t.Fatalf("no 'keep iterating' refusal was recorded on the transcript in cycle 1")
	}

	// ---- Cycle 2: more work, STILL unmet — the gate must block AGAIN. ----
	//
	// A gate that only refused the first Stop would let the agent stop on the
	// second while the target is still unmet — the exact failure a "don't stop
	// until X" rule exists to prevent. GateState is the authoritative "what did the
	// gate decide THIS cycle": BlockingErrorsFrom de-dupes identical refusal text
	// and accumulates across cycles, so cycle 1's block would still read in it.
	e.Run(proj, sess, "iterate again but still miss the target", Turns("done",
		Write("w2", "progress.txt", "tried harder, still short"),
	))
	// The context is still active — its exit read the gate's earlier fail and
	// stayed active, so the require is still satisfied for this cycle's gate.
	if active, _ := e.ContextState(proj, sess, "goal-tracking"); !active {
		t.Fatalf("the goal-tracking context deactivated while the target was still unmet — the gate would then never re-fire")
	}
	if status := e.GateState(proj, sess, "goal-verify"); status != "fail" {
		t.Fatalf("the goal-verify gate recorded %q on the second unmet Stop, want fail — the gate did not re-fire", status)
	}
	// The gate genuinely re-fired: the record gained MORE "keep iterating"
	// refusals in cycle 2. GateState=="fail" alone would also hold if cycle 1's
	// verdict were merely still on file and the gate never ran again; a strict
	// increase in the raw refusal count is what rules that out.
	blocksAfter2 := rawBlockRecords(t, e, proj, sess, keepIterating)
	if blocksAfter2 <= blocksAfter1 {
		t.Fatalf("the second unmet Stop did not add new 'keep iterating' refusals (after cycle 1: %d, after cycle 2: %d) — the gate did not re-fire",
			blocksAfter1, blocksAfter2)
	}

	// ---- Cycle 3: meet the target — the gate must ADMIT and the context deactivate. ----
	//
	// The verdict is read from GateState (the LAST verdict) and the context's active
	// flag, NOT from BlockingErrorsFrom, which still holds the earlier blocks.
	e.Run(proj, sess, "hit the target at last", Turns("done",
		Write("m3", "target-met", "accuracy=0.96"),
	))
	if status := e.GateState(proj, sess, "goal-verify"); status != "pass" {
		t.Fatalf("the goal-verify gate recorded %q after the target was met, want pass — the shipped verify.sh should admit the Stop", status)
	}
	if active, _ := e.ContextState(proj, sess, "goal-tracking"); active {
		t.Fatalf("the goal-tracking context stayed active after the goal was met — its exit should read the gate's pass and deactivate")
	}
	// The met Stop added NO new "keep iterating" refusals — the gate admitted this
	// cycle rather than blocking again. The complement of the cycle-2 increase:
	// together they show the gate blocks exactly while the target is unmet.
	if blocksAfter3 := rawBlockRecords(t, e, proj, sess, keepIterating); blocksAfter3 != blocksAfter2 {
		t.Fatalf("the met Stop still added 'keep iterating' refusals (after cycle 2: %d, after cycle 3: %d) — the gate should admit once the target is met",
			blocksAfter2, blocksAfter3)
	}
}

// T050_02: the control — a Stop with NO active goal is PERMITTED, not frozen.
//
// This is the control for T050_01: it proves cycle 1's block there came from
// verify.sh FAILING (the goal was declared but unmet), not from the gate refusing
// every Stop regardless of the goal. Here no goal.yaml is ever written, so the
// tracking context never enters — and the shipped gate's
// `match: context["goal-tracking"].active` makes it SKIP entirely, admitting the
// Stop. A session with no goal has nothing to iterate toward, so its Stop must be
// left alone; that the gate refuses ONLY when a goal is genuinely active-and-unmet
// (T050_01) and not otherwise is exactly what distinguishes the loop from a gate
// that blocks unconditionally.
//
// (The example was fixed on 2026-08-20 after this suite first surfaced that a bare
// `require:[{context}]` BLOCKS an unmet dependency — the engine refuses an unmet
// require, internal/dispatch/require.go checkContext — which would freeze every
// goal-free Stop and never run the check. Adding the same match-skip the sibling
// keyword-coverage-registry gate already uses realizes run-verify.sh's own
// "context not active — permit the Stop" intent, which was previously unreachable.)
func TestT050_02_NoActiveGoalIsPermitted(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)

	sess := "s-050-02"
	res := e.Run(proj, sess, "do unrelated work and stop", Turns("done",
		Write("w1", "notes.txt", "no goal declared here"),
	))

	// The gate skips (its match reads goal-tracking inactive), so the Stop is not
	// refused at all — nothing blocks.
	if res.Refused() {
		t.Fatalf("a goal-free Stop was refused, but the gate must skip when no goal is active:\n%s", res.Output)
	}
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) != 0 {
		t.Fatalf("a goal-free Stop produced blocking errors, but the gate must skip when no goal is active:\n%v", blocks)
	}
	// In particular verify.sh must never run when no goal was declared — its "keep
	// iterating" sentence must be absent.
	if containsAny(blocks, keepIterating) {
		t.Fatalf("a goal-free Stop hit verify.sh's 'keep iterating' — the gate must skip, not run the check:\n%v", blocks)
	}
	// The tracking context never activated, since no goal.yaml was written.
	if active, _ := e.ContextState(proj, sess, "goal-tracking"); active {
		t.Fatalf("the goal-tracking context is active though no goal.yaml was ever written")
	}
}
