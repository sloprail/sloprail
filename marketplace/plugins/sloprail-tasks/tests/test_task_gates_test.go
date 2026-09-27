package e2e

import "testing"

// task-gates-hold is a PREVENTIVE file-guard over
// memories/tasks/<cat>/<name>/TASK.md with TWO checks in order:
//
//  1. SCRIPT (gates-hold.sh): every gates/*.sh under the task's own gates/
//     directory is run, in name order. exit 0 passes; the first failing (or
//     non-executable) gate refuses, naming it. .md files are untouched here.
//  2. PREPARE + JUDGE: reached only once every .sh gate passed. Collects every
//     gates/*.md file's text and asks the model whether each currently holds.
//
// Like task-dependencies-resolve, this only matters on the transition OUT of
// backlog/blocked INTO to_do/in_progress.
//
// This guard TRUSTS that everything under gates/ already cleared the grounding
// bar (task-gate-is-grounded's job, at write time) -- it does not itself judge
// whether a gate is a MEANINGFUL test, only whether it currently holds.
//
// These prove: moving to in_progress with a failing .sh gate is refused, and
// permitted once the gate passes.

// gatesTaskPath and its sibling gate file path, under a group distinct from
// taskPath's so this file's tests never collide with another test file's task.
const (
	gatesTaskPath = "memories/tasks/web/launch-page/TASK.md"
	gateShPath    = "memories/tasks/web/launch-page/gates/repo-is-public.sh"
)

// failingGate always exits 1 -- the condition never holds.
const failingGate = "#!/usr/bin/env bash\nexit 1\n"

// passingGate always exits 0 -- the condition always holds.
const passingGate = "#!/usr/bin/env bash\nexit 0\n"

// TestGates_FailingScriptGateBlocksThenPassingPermits: a task moving to
// in_progress with a gates/*.sh that fails is refused; once the SAME gate file
// is rewritten to pass, the identical task write is permitted. The gate write
// itself needs task-gate-is-grounded to admit it (its own test file covers
// that guard specifically); here the body is grounded from the start so the
// gate file lands, isolating task-gates-hold's own transition refusal.
func TestGates_FailingScriptGateBlocksThenPassingPermits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-gates-sh"
	body := askBody
	backlogDoc := task("backlog", "P1", body)

	// Land the task (backlog) and the failing gate in one run, so the gate
	// write itself is judged (grounded, real content) and then the task
	// enters backlog cleanly.
	res0 := e.Run(proj, sess, authPrompt, Turns("done",
		srWrite("b0", gatesTaskPath, backlogDoc, citeUser(askQuote)),
		Write("w1", gateShPath, failingGate),
	))
	if res0.Refused() {
		t.Fatalf("landing the task and its gate at backlog was refused (setup broken):\n%s", res0.Output)
	}

	toDoDoc := task("to_do", "P1", body)
	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w2", gatesTaskPath, toDoDoc),
	))
	if !res.Refused() {
		t.Fatalf("moving to to_do with a failing gate was not refused:\n%s", res.Output)
	}
	if !res.Saw("GATES NOT SATISFIED") {
		t.Errorf("the refusal was not the gates-not-satisfied reason:\n%s", res.Output)
	}

	// Rewrite the SAME gate file to pass.
	res2 := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w3", gateShPath, passingGate),
	))
	if res2.Refused() {
		t.Fatalf("rewriting the gate to pass was itself refused (setup broken):\n%s", res2.Output)
	}

	res3 := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w4", gatesTaskPath, toDoDoc),
	))
	if res3.Refused() {
		t.Fatalf("moving to to_do with a passing gate was refused:\n%s", res3.Output)
	}
}

// TestGates_JudgmentGateInvoked: a gates/*.md judgment gate is present, and the
// stubbed judge decides the outcome -- pass:true permits the transition,
// pass:false refuses it with the reasoning reaching the agent. This proves the
// judge is actually invoked for a .md gate (not silently skipped), by flipping
// the SAME stub between the two sub-tests and observing the opposite verdicts.
func TestGates_JudgmentGateInvoked(t *testing.T) {
	const gateMdPath = "memories/tasks/web/launch-page/gates/launch-video-exists.md"
	const judgmentGate = "The launch video exists at memories/launch/video.mp4 and shows a working demo of the product.\n"

	t.Run("judge_fail_refuses", func(t *testing.T) {
		e := New(t)
		proj := e.Project()
		e.GitInit(proj)
		installPluginTree(t, e, proj)

		// The harness gives ONE stubbed verdict to EVERY judge in a run (see
		// tests/README.md, "What is stubbed, and why"). Landing the task body
		// AND the gate file each fire their OWN judges (task-body-is-human-
		// authored's, task-gate-is-grounded's) -- so SETUP is done with the
		// stub at pass:true, and the stub is flipped to pass:false only for
		// the FINAL call under test, which writes nothing but the TASK.md
		// status change and so invokes ONLY task-gates-hold's judge. This is
		// what isolates task-gates-hold's own verdict from its two siblings'.
		e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

		sess := "s-gates-md-fail"
		body := askBody
		backlogDoc := task("backlog", "P1", body)

		res0 := e.Run(proj, sess, authPrompt, Turns("done",
			srWrite("b0", gatesTaskPath, backlogDoc, citeUser(askQuote)),
			Write("w1", gateMdPath, judgmentGate),
		))
		if res0.Refused() {
			t.Fatalf("landing the task and its judgment gate was refused (setup broken):\n%s", res0.Output)
		}

		// Flip the stub -- only the transition attempt below sees pass:false,
		// and it is the only remaining write, so task-gates-hold's judge is
		// the only one this verdict can apply to.
		e.InstallJudgeClaude(`{"pass": false, "reasoning": "GATES: the launch video does not exist."}`)

		toDoDoc := task("to_do", "P1", body)
		res := e.Run(proj, sess, authPrompt, Turns("done",
			Write("w2", gatesTaskPath, toDoDoc),
		))
		if !res.Refused() {
			t.Fatalf("a judgment gate the judge FAILS did not refuse the transition:\n%s", res.Output)
		}
		if !res.Saw("does not exist") {
			t.Errorf("the judge's reasoning did not reach the agent:\n%s", res.Output)
		}
	})

	t.Run("judge_pass_permits", func(t *testing.T) {
		e := New(t)
		proj := e.Project()
		e.GitInit(proj)
		installPluginTree(t, e, proj)
		e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

		sess := "s-gates-md-pass"
		body := askBody
		backlogDoc := task("backlog", "P1", body)

		res0 := e.Run(proj, sess, authPrompt, Turns("done",
			srWrite("b0", gatesTaskPath, backlogDoc, citeUser(askQuote)),
			Write("w1", gateMdPath, judgmentGate),
		))
		if res0.Refused() {
			t.Fatalf("landing the task and its judgment gate was refused (setup broken):\n%s", res0.Output)
		}

		toDoDoc := task("to_do", "P1", body)
		res := e.Run(proj, sess, authPrompt, Turns("done",
			Write("w2", gatesTaskPath, toDoDoc),
		))
		if res.Refused() {
			t.Fatalf("a judgment gate the judge PASSES refused the transition:\n%s", res.Output)
		}
	})
}
