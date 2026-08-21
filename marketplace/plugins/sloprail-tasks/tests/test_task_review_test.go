package e2e

import "testing"

// task-review is an AFTER-CHECK file-guard (NO preventive) over
// memories/tasks/<cat>/<name>/TASK.md — it fires at the Post/Stop after-check on
// the SETTLED file, never at Pre. It reviews a task written into `in_review`: a
// SCRIPT pre-flight (only in_review; every [quote](jsonl) evidence link must
// GROUND), then a PREPARE + JUDGE that decides whether the evidence substantiates
// the claim. The judge FAILING refuses the turn (REVIEW REJECTED); the task stays
// in_review, and because a Post refusal does not advance the read mark it re-fires
// next cycle until fixed.
//
// # Why some of these tests disable the sibling guards
//
// task-review shares its path with two PREVENTIVE guards, task-evidence-resolves
// and task-body-is-human-authored, which fire at PRE when the in_review TASK.md is
// written. task-body has its OWN judge (its stage 2). The harness's single judge
// stub (InstallJudgeClaude) gives ONE verdict to every judge, so a test that needs
// task-review's judge to FAIL while task-body's judge PASSES cannot express that
// with one stub. Where that conflict arises, the test DISABLES task-body (via the
// project config's `disabled:` list) so the one stubbed verdict is task-review's
// alone — a focused isolation, documented per test. task-evidence is deterministic
// (no judge) and is left enabled: its grounded-evidence Pre check passes, so the
// in_review write lands and reaches task-review at Stop.
//
// These prove: an in_review task the review judge ACCEPTS permits (the review runs
// at the Post/Stop after-check and does not block); an in_review task the review
// judge REJECTS is BLOCKED at Stop with the rejection reaching the agent; and an
// in_review task whose evidence does not ground is refused by the pre-flight
// (deterministically) — which also happens to be what task-evidence catches at
// Pre, so this shows task-review's own pre-flight refuses when reached.

// TestReview_SubstantiatedPermits: an in_review task with grounded evidence that
// the review judge accepts (pass:true) permits — the review runs at Stop and does
// NOT block. All guards enabled: task-evidence and task-body pass at Pre (grounded
// + judge PASS), task-review passes at Stop. This is the control: without it, a
// review that blocked everything would pass the reject test below.
func TestReview_SubstantiatedPermits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-review-ok"
	tp := e.TranscriptPath(proj, sess)
	body := "Done. The user asked to " + cite("migrate the auth module", tp, 1) + "."

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, task("in_review", "P1", body)),
	))

	if res.Refused() {
		t.Fatalf("a substantiated in_review task write was refused at Pre:\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("a substantiated in_review task was blocked at Stop:\n%v", blocks)
	}
	if !e.Exists(proj, taskPath) {
		t.Errorf("the in_review task did not land on disk")
	}
}

// TestReview_UnsubstantiatedBlocksAtStop: an in_review task the review judge
// REJECTS is blocked at Stop, and the rejection reaches the agent. task-body is
// disabled so the single stubbed verdict (pass:false) is task-review's alone;
// task-evidence stays enabled and its grounded-evidence Pre check passes, so the
// write lands and task-review judges it at Stop. This is the "a not-fine task
// re-fires/blocks at Stop" behaviour the after-check exists for.
func TestReview_UnsubstantiatedBlocksAtStop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	// Isolate task-review's judge: task-body's Pre judge would otherwise share the
	// pass:false stub and refuse the write before it could reach Stop.
	e.DisableFileGuard(proj, "task-body-is-human-authored")
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "REVIEW REJECTED: the cited evidence shows the task was asked for but nothing proving the migration was actually done"}`)

	sess := "s-review-reject"
	tp := e.TranscriptPath(proj, sess)
	body := "Done. The user asked to " + cite("migrate the auth module", tp, 1) + "."

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, task("in_review", "P1", body)),
	))
	_ = res

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("an unsubstantiated in_review task was not blocked at Stop:\n%s", res.Output)
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	if !containsStr(joined, "nothing proving the migration was actually done") {
		t.Errorf("the review judge's rejection reasoning did not reach the agent at Stop:\n%s", joined)
	}
}

// TestReview_UngroundedEvidenceRefusedByPreflight: an in_review task whose evidence
// citation does not ground is refused deterministically — task-review's pre-flight
// refuses when reached, without a model. Here task-evidence (also enabled) refuses
// it at Pre first (its Pre check grounds the same citation), so the write never
// lands; the point proven is that an ungrounded in_review task does not slip
// through to an approval — a fabricated citation cannot substantiate a claim. The
// judge stub is PASS to show the DETERMINISTIC layer is what refuses, not a judge.
func TestReview_UngroundedEvidenceRefusedByPreflight(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-review-ungrounded"
	tp := e.TranscriptPath(proj, sess)
	// A fabricated quote — cite must not resolve it.
	body := "Done. " + cite("deployed to production and verified all metrics green", tp, 0)

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, task("in_review", "P1", body)),
	))

	if !res.Refused() {
		t.Fatalf("an in_review task with ungrounded evidence was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("an ungrounded in_review task landed on disk")
	}
	if !res.Saw("resolves to nothing the user said") {
		t.Errorf("the refusal was not the citation-grounding reason:\n%s", res.Output)
	}
}
