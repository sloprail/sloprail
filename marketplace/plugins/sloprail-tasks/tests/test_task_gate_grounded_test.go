package e2e

import "testing"

// task-gate-is-grounded is a PREVENTIVE file-guard over
// memories/tasks/<cat>/<name>/gates/<gate-name>.{sh,md} with TWO checks:
//
//  1. SCRIPT (has-cited-body.sh): the gate's sibling TASK.md must carry at
//     least one grounded [quote](jsonl) body citation. No citation, or one
//     that does not ground: refused HERE, before the judge.
//  2. PREPARE + JUDGE: the gate must be traceable to the cited ask, must not
//     contradict it, must not invent beyond it, and -- for a .sh gate -- must
//     not be TRIVIAL (a script whose control flow can never actually fail).
//
// Being preventive, a not-fine gate write is refused at PRE-tool, before it
// lands.
//
// These prove: a gate whose sibling task has no grounded citation is refused
// by the deterministic stage before any judge call; and the judge itself is
// what refuses a gate it finds ungrounded/trivial/contradicting and admits one
// it finds grounded -- proven by flipping the SAME stubbed verdict.

const (
	groundedTaskPath = "memories/tasks/web/launch-site/TASK.md"
	groundedGatePath = "memories/tasks/web/launch-site/gates/repo-is-public.sh"
)

// TestGateGrounded_UngroundedTaskBodyRefusedBeforeJudge: the sibling TASK.md
// has NO citation at all (no [quote](jsonl) link), so there is nothing for a
// gate to trace to. Refused by the deterministic stage 1 -- proven by NOT
// stubbing the judge to PASS (if stage 1 let this through, the write would
// still be refused by the engine's judge machinery failing closed with no
// stub installed, but the assertion below specifically checks for stage 1's
// own reason text, which only stage 1 emits).
func TestGateGrounded_UngroundedTaskBodyRefusedBeforeJudge(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)

	sess := "s-gate-grounded-nobody"

	// The task body carries NO citation -- just prose.
	ungroundedDoc := "---\nstatus: backlog\npriority: P1\n---\n\nSomething to do, unattributed.\n"

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", groundedTaskPath, ungroundedDoc),
	))
	// The task write itself is refused by task-body-is-human-authored (its own
	// subject), so land it via a path that bypasses that guard's own concerns:
	// disable task-body so ONLY the ungrounded-body fact (no citation to trace
	// a gate against) is what this test exercises on the GATE write.
	_ = res // the task write's own refusal is not this test's subject.

	e2 := New(t)
	proj2 := e2.Project()
	e2.GitInit(proj2)
	installPluginTree(t, proj2)
	e2.DisableFileGuard(proj2, "task-body-is-human-authored")

	sess2 := "s-gate-grounded-nobody-2"
	res2 := e2.Run(proj2, sess2, authPrompt, Turns("done",
		Write("w1", groundedTaskPath, ungroundedDoc),
	))
	if res2.Refused() {
		t.Fatalf("landing the ungrounded task (task-body disabled) was itself refused (setup broken):\n%s", res2.Output)
	}

	res3 := e2.Run(proj2, sess2, authPrompt, Turns("done",
		Write("w2", groundedGatePath, passingGate),
	))
	if !res3.Refused() {
		t.Fatalf("a gate whose sibling task has no grounded citation was not refused:\n%s", res3.Output)
	}
	if e2.Exists(proj2, groundedGatePath) {
		t.Errorf("the preventive guard let an ungrounded gate land")
	}
	if !res3.Saw("NO GROUNDED ASK") {
		t.Errorf("the refusal was not the no-grounded-ask reason:\n%s", res3.Output)
	}
}

// TestGateGrounded_JudgeRefusesUngroundedOrTrivialGate: the sibling task HAS a
// grounded citation (stage 1 passes), but the judge rejects the gate itself
// (ungrounded, contradicting, invented, or trivial -- the judge is stubbed, so
// this proves the REFUSAL PATH reaches the agent, not any one specific
// judgement).
func TestGateGrounded_JudgeRefusesUngroundedOrTrivialGate(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)

	// The harness gives ONE stubbed verdict to EVERY judge in a run. Landing
	// the task body fires task-body-is-human-authored's OWN judge, so setup
	// is done at pass:true and the stub is flipped to pass:false only for the
	// gate write under test -- which is task-gate-is-grounded's judge alone.
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-gate-grounded-trivial"
	tp := e.TranscriptPath(proj, sess)
	body := "The user asked to " + cite("migrate the auth module", tp, 1) + "."
	groundedDoc := "---\nstatus: backlog\npriority: P1\n---\n\n" + body + "\n"

	res0 := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", groundedTaskPath, groundedDoc),
	))
	if res0.Refused() {
		t.Fatalf("landing the grounded task was itself refused (setup broken):\n%s", res0.Output)
	}

	e.InstallJudgeClaude(`{"pass": false, "reasoning": "GATE: this .sh gate always exits 0 and can never fail -- it is not a real condition."}`)

	// A gate that is a bare, unconditional pass -- the trivial shape the judge
	// is stubbed to reject.
	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w2", groundedGatePath, passingGate),
	))
	if !res.Refused() {
		t.Fatalf("a gate the judge rejects (trivial/ungrounded) was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, groundedGatePath) {
		t.Errorf("the preventive guard let a judge-rejected gate land")
	}
	if !res.Saw("can never fail") {
		t.Errorf("the judge's reasoning did not reach the agent:\n%s", res.Output)
	}
}

// TestGateGrounded_JudgePermitsGroundedGate: the same grounded task, the same
// gate shape, but the judge PASSES -- the control proving the refusal above is
// about the judge's verdict, not about the gate path or shape being refused
// unconditionally.
func TestGateGrounded_JudgePermitsGroundedGate(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-gate-grounded-ok"
	tp := e.TranscriptPath(proj, sess)
	body := "The user asked to " + cite("migrate the auth module", tp, 1) + "."
	groundedDoc := "---\nstatus: backlog\npriority: P1\n---\n\n" + body + "\n"

	res0 := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", groundedTaskPath, groundedDoc),
	))
	if res0.Refused() {
		t.Fatalf("landing the grounded task was itself refused (setup broken):\n%s", res0.Output)
	}

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w2", groundedGatePath, passingGate),
	))
	if res.Refused() {
		t.Fatalf("a gate the judge PASSES was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, groundedGatePath) {
		t.Errorf("an admitted gate write did not land on disk")
	}
}
