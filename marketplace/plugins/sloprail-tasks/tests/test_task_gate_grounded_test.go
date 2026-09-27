package e2e

import "testing"

// task-gate-is-grounded is a PREVENTIVE file-guard over
// memories/tasks/<cat>/<name>/gates/<gate-name>.{sh,md} with ONE check: a
// JUDGE, no script stage. A gate carries NO citations of its own -- only the
// write that creates a TASK.md or changes its body cites the user's words, and
// that grounding is task-body-is-human-authored's subject, validated
// separately. So this guard's prepare hands the judge the gate
// file's own content and the sibling TASK.md's full content as it stands
// (frontmatter and body, unmodified) -- no second citation-extraction
// pipeline. The judge decides whether the gate is DERIVED from the task:
// traceable to it, not contradicting it, not inventing beyond it, and --
// for a .sh gate -- not TRIVIAL (a script whose control flow can never
// actually fail).
//
// Being preventive, a not-fine gate write is refused at PRE-tool, before it
// lands.
//
// These prove: the judge is what refuses a gate it finds
// untraceable/trivial/contradicting and admits one it finds derived from the
// task -- proven by flipping the SAME stubbed verdict; and the judge is
// invoked (and can reject) even when the task was written with no citation,
// because the grounding question belongs to a different guard entirely and
// is not this one's business.

const (
	groundedTaskPath = "memories/tasks/web/launch-site/TASK.md"
	groundedGatePath = "memories/tasks/web/launch-site/gates/repo-is-public.sh"
)

// TestGateGrounded_JudgeRunsEvenWithoutTaskBodyCitation: the sibling TASK.md
// is written with NO citation at all (the plain Write tool) --
// task-body-is-human-authored is disabled so that fact does not itself refuse
// the task write (that guard's
// own subject is tested elsewhere; this test isolates task-gate-is-grounded).
// With no script stage of its own, task-gate-is-grounded's judge still runs
// on the gate write and can refuse it based on the gate's relationship to the
// task's content -- proving citation-grounding is no longer this guard's
// question, only DERIVATION is.
func TestGateGrounded_JudgeRunsEvenWithoutTaskBodyCitation(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	// The guard is PLUGIN-shipped (see installPluginTree), so disabling it
	// needs the qualified form <plugin>/<nature>/<name>.
	e.DisablePluginGuardrail(proj, pluginName+"/file-guard/task-body-is-human-authored")
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "GATE: this condition has nothing to do with what the task describes."}`)

	sess := "s-gate-grounded-nocitation"
	// The task body carries NO citation -- just prose. Admitted because
	// task-body-is-human-authored is disabled above.
	ungroundedDoc := "---\nstatus: backlog\npriority: P1\n---\n\nSomething to do, unattributed.\n"

	res0 := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", groundedTaskPath, ungroundedDoc),
	))
	if res0.Refused() {
		t.Fatalf("landing the uncited task (task-body disabled) was itself refused (setup broken):\n%s", res0.Output)
	}

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w2", groundedGatePath, passingGate),
	))
	if !res.Refused() {
		t.Fatalf("the judge did not run (or did not refuse) on a gate under an uncited task -- task-gate-is-grounded should not depend on the task body carrying a citation:\n%s", res.Output)
	}
	if e.Exists(proj, groundedGatePath) {
		t.Errorf("the preventive guard let a judge-rejected gate land")
	}
	if !res.Saw("nothing to do with what the task describes") {
		t.Errorf("the judge's reasoning did not reach the agent:\n%s", res.Output)
	}
}

// TestGateGrounded_JudgeRefusesUntraceableOrTrivialGate: the sibling task
// exists (created with a cited ask, so task-body's own judge passes it), but the
// task-gate-is-grounded judge rejects the gate itself (untraceable,
// contradicting, invented, or trivial -- the judge is stubbed, so this
// proves the REFUSAL PATH reaches the agent, not any one specific
// judgement).
func TestGateGrounded_JudgeRefusesUntraceableOrTrivialGate(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)

	// The harness gives ONE stubbed verdict to EVERY judge in a run. Landing
	// the task body fires task-body-is-human-authored's OWN judge, so setup
	// is done at pass:true and the stub is flipped to pass:false only for the
	// gate write under test -- which is task-gate-is-grounded's judge alone.
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-gate-grounded-trivial"
	groundedDoc := task("backlog", "P1", askBody)

	res0 := e.Run(proj, sess, authPrompt, Turns("done",
		srWrite("b1", groundedTaskPath, groundedDoc, citeUser(askQuote)),
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
		t.Fatalf("a gate the judge rejects (untraceable/trivial) was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, groundedGatePath) {
		t.Errorf("the preventive guard let a judge-rejected gate land")
	}
	if !res.Saw("can never fail") {
		t.Errorf("the judge's reasoning did not reach the agent:\n%s", res.Output)
	}
}

// TestGateGrounded_JudgePermitsDerivedGate: the same grounded task, the same
// gate shape, but the judge PASSES -- the control proving the refusal above
// is about the judge's verdict, not about the gate path or shape being
// refused unconditionally.
func TestGateGrounded_JudgePermitsDerivedGate(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-gate-grounded-ok"
	groundedDoc := task("backlog", "P1", askBody)

	res0 := e.Run(proj, sess, authPrompt, Turns("done",
		srWrite("b1", groundedTaskPath, groundedDoc, citeUser(askQuote)),
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
