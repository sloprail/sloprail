package e2e

import "testing"

// task-body-is-human-authored is a PREVENTIVE file-guard over
// memories/tasks/<cat>/<name>/TASK.md with TWO checks in order:
//
//   1. SCRIPT (has-body-citation.sh): the body must carry a [quote](jsonl) link
//      whose quote GROUNDS via cite to the user's own words. No citation, or one
//      that does not ground, is refused HERE — deterministically, before the judge.
//   2. PREPARE + JUDGE: the body must correspond to the cited messages and hold
//      THAT AND NOTHING ELSE. The judge is the model; its verdict is stubbed.
//
// Being preventive, a not-fine write is refused at PRE-tool, before it lands.
//
// The judge verdict is a fixed stub (InstallJudgeClaude) — pass:true admits,
// pass:false refuses and the judge's reasoning reaches the agent — the same
// substitution the main suite's judge e2e make. The DETERMINISTIC stage 1 and
// cite's grounding are NOT stubbed and run for real against the seeded transcript.
//
// These prove: a human-authored body (grounded citation + judge PASS) ADMITS and
// lands; a slop / AI-looking body the judge rejects (grounded citation + judge
// FAIL) is REFUSED and the reasoning reaches the agent; and a body with no citation
// is refused by the SCRIPT stage before the judge is ever asked (stub set to PASS
// to prove the script, not the judge, is what refused).

// TestBody_HumanAuthoredPasses: a body that quotes and links the human's own ask
// (grounded) and that the judge accepts (pass:true) admits, and the file lands.
// This is the happy path and the control for the refusals below.
func TestBody_HumanAuthoredPasses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-body-ok"
	tp := e.TranscriptPath(proj, sess)
	body := "The user asked to " + cite("migrate the auth module", tp, 1) + "."

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, task("to_do", "P1", body)),
	))

	if res.Refused() {
		t.Fatalf("a human-authored, judge-accepted body was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, taskPath) {
		t.Errorf("an admitted body write did not land on disk")
	}
}

// TestBody_SlopBodyRefusedByJudge: a body carrying a grounded citation but ALSO
// agent-authored elaboration the human never asked for — the "and nothing else"
// violation — passes stage 1 (the citation grounds) and is then REFUSED by the
// judge (stub pass:false), with the judge's reasoning reaching the agent. The
// preventive guard keeps it off disk.
func TestBody_SlopBodyRefusedByJudge(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "TASK BODY: the body adds acceptance criteria and a suggested approach the user never stated"}`)

	sess := "s-body-slop"
	tp := e.TranscriptPath(proj, sess)
	// A grounded citation wrapped in invented scope — the judge's job to reject.
	body := "The user asked to " + cite("migrate the auth module", tp, 1) +
		". Acceptance criteria: 100% test coverage, a rollback plan, and a metrics dashboard. Suggested approach: strangler-fig migration over three sprints."

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, task("to_do", "P1", body)),
	))

	if !res.Refused() {
		t.Fatalf("a slop body the judge rejected was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("the preventive guard let a judge-rejected body land on disk")
	}
	if !res.Saw("acceptance criteria and a suggested approach the user never stated") {
		t.Errorf("the judge's reasoning did not reach the agent:\n%s", res.Output)
	}
}

// TestBody_NoCitationRefusedByScript: a body with no citation at all is refused by
// the deterministic SCRIPT stage, before the judge. The stub is set to PASS: if the
// engine ever reached the judge here (it must not — the script refuses first), or
// admitted, the write would go through. It does not, and the refusal is the
// script's own missing-citation reason.
func TestBody_NoCitationRefusedByScript(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-body-nocite"
	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, task("to_do", "P1", "Migrate the auth module. (a body an agent could have written, no citation)")),
	))

	if !res.Refused() {
		t.Fatalf("a body with no citation was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("the preventive guard let a citation-less body land on disk")
	}
	// The SCRIPT tier's own reason, not a judge verdict.
	if !res.Saw("carries no citation of a user message") {
		t.Errorf("the refusal was not the script tier's missing-citation reason:\n%s", res.Output)
	}
}
