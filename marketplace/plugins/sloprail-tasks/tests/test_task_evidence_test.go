package e2e

import "testing"

// task-evidence-resolves is a PREVENTIVE file-guard over
// memories/tasks/<cat>/<name>/TASK.md with one deterministic SCRIPT check: the
// frontmatter must satisfy task.cue, and every [quote](jsonl) evidence link must
// GROUND via `sr-session trajectory cite` to the user's own words. Being
// preventive, a not-fine write is refused at PRE-tool, before it lands.
//
// task-evidence-resolves's OWN check is deterministic — no judge. But
// task-body-is-human-authored guards the SAME path and DOES have a judge (its
// stage 2), so every TASK.md write in these tests also fires that judge. So a
// passing judge stub (InstallJudgeClaude pass:true) is installed wherever the body
// carries a grounded citation, to let task-body permit and isolate what
// task-evidence does. The one test that needs NO stub is the fabricated-citation
// one: there task-body's own deterministic stage 1 refuses before its judge, so
// the model is never asked.
//
// These prove: a task with a grounded citation PERMITS and lands; a task whose
// citation quote is fabricated is REFUSED (cite finds no match); an
// invalid-frontmatter task (status: done) is refused by the schema; and an
// in_review task with no citation at all is refused for missing evidence.

// TestEvidence_GroundedCitationPermits: a to_do task whose body cites the human's
// own words with a resolvable [quote](jsonl:line) link permits, and the file lands.
// The quote is a substring of authPrompt, which the harness seeded as the
// transcript's line-1 user message — so cite grounds it. This is the control for
// every refusal below: without it, a guard refusing everything would pass them.
func TestEvidence_GroundedCitationPermits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	// The body carries a grounded citation, so task-body's stage 1 passes and its
	// judge runs — stub it PASS so the only thing that could refuse is
	// task-evidence, which must not.
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-evidence-ok"
	tp := e.TranscriptPath(proj, sess)
	body := "The user asked to " + cite("migrate the auth module", tp, 1) + "."

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, task("to_do", "P1", body)),
	))

	if res.Refused() {
		t.Fatalf("a task with a grounded citation was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, taskPath) {
		t.Errorf("an admitted task write did not land on disk")
	}
}

// TestEvidence_FabricatedCitationRefused: a task whose citation quote is NOT the
// user's words is refused at pre-tool — cite finds no match, and the deterministic
// script refuses naming the quote. The write never lands (preventive).
func TestEvidence_FabricatedCitationRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)

	sess := "s-evidence-fake"
	tp := e.TranscriptPath(proj, sess)
	// A quote the user never said — cite must not resolve it.
	body := "The user asked to " + cite("rewrite the entire billing system from scratch", tp, 0) + "."

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, task("to_do", "P1", body)),
	))

	if !res.Refused() {
		t.Fatalf("a task with a fabricated citation was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("the preventive guard let a fabricated-citation task land on disk")
	}
	if !res.Saw("resolves to nothing the user said") {
		t.Errorf("the refusal was not the citation-grounding reason:\n%s", res.Output)
	}
}

// TestEvidence_InvalidFrontmatterRefused: a task with status `done` — a status the
// schema does not have — is refused by the schema check, before any citation is
// looked at. This is the "there is no done" invariant the whole task lifecycle
// rests on.
func TestEvidence_InvalidFrontmatterRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	// A grounded citation, so task-body's stage 1 passes and its judge would run —
	// stub it PASS so the refusal can ONLY be task-evidence's schema check.
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-evidence-schema"
	tp := e.TranscriptPath(proj, sess)
	// A grounded citation, so ONLY the schema can be what refuses this.
	body := "Done. " + cite("migrate the auth module", tp, 1)

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, task("done", "P1", body)),
	))

	if !res.Refused() {
		t.Fatalf("a task with an invalid status was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("the preventive guard let a status:done task land on disk")
	}
	// The schema check is what must refuse this — its message names the CUE
	// failure. (task-body's judge is stubbed PASS, so it cannot be the refuser.)
	if !res.Saw("does not satisfy .sloprail/schemas/task.cue") {
		t.Errorf("the refusal was not task-evidence's schema reason:\n%s", res.Output)
	}
}

// TestEvidence_InReviewNeedsEvidence: an in_review task with NO citation at all is
// refused for missing evidence — a claim of finished work must ground itself. This
// is the deterministic half of the in_review requirement (task-review's judge is
// the other half).
func TestEvidence_InReviewNeedsEvidence(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)

	sess := "s-evidence-review-noev"
	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, task("in_review", "P1", "The work is finished. (no citation)")),
	))

	if !res.Refused() {
		t.Fatalf("an in_review task with no evidence was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("the preventive guard let an evidence-less in_review task land")
	}
	if !res.Saw("carries no citation") {
		t.Errorf("the refusal was not the missing-evidence reason:\n%s", res.Output)
	}
}
