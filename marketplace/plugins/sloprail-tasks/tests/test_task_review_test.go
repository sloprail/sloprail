package e2e

import (
	"strings"
	"testing"
)

// task-review is an AFTER-CHECK file-guard (NO preventive) over
// memories/tasks/<cat>/<name>/TASK.md — it fires at the Post/Stop after-check on the
// SETTLED file, never at Pre. It reviews a task written into `in_review`, and it
// proves DELIVERY, not the ask: a SCRIPT pre-flight (only in_review; every
// FRONTMATTER observation must resolve to a tool_result line and every artifact to
// real tree lines), then a PREPARE + JUDGE that decides whether that delivered
// evidence SUBSTANTIATES the claim. The judge FAILING refuses the turn (REVIEW
// REJECTED); the task stays in_review, and because a Post refusal does not advance
// the read mark it re-fires next cycle until fixed.
//
// # The corrected concept (was conflated with task-body)
//
// task-review used to re-cite the USER's words — a duplicate of
// task-body-is-human-authored. That was wrong: a review proves the work was DONE.
// The evidence it now weighs is DELIVERY evidence, in the task's FRONTMATTER:
//
//   observations: ["/abs/session.jsonl:120"]   proof it happened — the cited line
//                                               is a tool_result the session produced
//   artifacts:    ["src/auth.go:10-40"]         where the result is — repo-relative
//                                               tree files at the lines that changed
//
// So a passing case cites a REAL tool_result (an absolute .jsonl line) for its
// observation AND a REAL repo-relative file:line for its artifact — the delivery,
// not the ask. These are driven through the MOCK: a tool_result is produced by
// harness.ToolResult (persisted as a real tool_result record, #470), and the
// artifact file is written into the tree by a Write turn; the observation cites the
// tool_result's real line (read from the transcript the mock wrote).
//
// # Why some tests disable the sibling body guard
//
// task-review shares its path with task-body-is-human-authored, which has its OWN
// judge (stage 2) and fires at Pre. The harness's single judge stub gives ONE
// verdict to every judge, so a test needing task-review's judge to FAIL while
// task-body PASSES cannot express that with one stub. Where that conflict arises the
// test DISABLES task-body so the one stubbed verdict is task-review's alone.
// task-evidence-resolves is deterministic (no judge) and stays enabled: its
// resolve-the-evidence Pre check passes for real evidence, so the in_review write
// lands and reaches task-review at Stop.
//
// # The two-run shape (why the evidence exists before the task is written)
//
// The observation cites a transcript line that must ALREADY be a tool_result when
// task-evidence-resolves checks the in_review write at Pre. So the evidence is
// produced in a FIRST run (a ToolResult turn + the artifact Write), whose tool_result
// line is then read from the transcript; a SECOND run on the same session (a resume)
// writes the in_review task citing that now-known line and the artifact file. This is
// the honest shape — the work happens, THEN the task claims it — and the same
// same-session-resume the cite suite uses for a genuine multi-turn transcript.

// prepareDelivery drives run 1: it produces a tool_result carrying resultMarker and
// writes the artifact file at artifactPath, then returns the tool_result's physical
// line in the transcript so run 2 can cite it. The artifact file is written with
// enough lines that a :range into it resolves.
func prepareDelivery(t *testing.T, e *Env, proj, sess, resultMarker, artifactPath string) int {
	t.Helper()
	// Write FIRST — a real tool_use the mock executes, so the artifact file lands in
	// the tree — THEN the ToolResult carrying the marker. (A ToolResult turn is a
	// user record; when it comes first the mock reaches end-of-turn before the Write
	// tool_use fires, so the file would never be created. Write-first fixes that, and
	// its own success tool_result lands too — harmless, a different marker.)
	e.Run(proj, sess, authPrompt, Turns("done",
		Write("wf", artifactPath, "package auth\n\n// migrated to the new token format\nfunc Migrate() error {\n\treturn nil\n}\n"),
		ToolResult("r1", "go test ./auth/...\nok  sloprail/auth  0.42s\n"+resultMarker),
	))
	tp := e.TranscriptPath(proj, sess)
	line := toolResultLine(t, tp, resultMarker)
	if line == 0 {
		t.Fatalf("run 1 did not put the tool_result on the transcript:\n%s", transcriptText(t, tp))
	}
	return line
}

// TestReview_SubstantiatedPermits: an in_review task whose observation cites a REAL
// tool_result (the test that ran green) AND whose artifact cites the REAL produced
// file, and that the review judge accepts (pass:true), permits — the review runs at
// Stop and does NOT block. This is the control and, crucially, the case that proves
// the review is about DELIVERY: the evidence is the tool result and the changed
// file, not a re-citation of the user's ask.
func TestReview_SubstantiatedPermits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-review-ok"
	artifactRel := "src/auth.go"
	line := prepareDelivery(t, e, proj, sess, "PASS", artifactRel)
	tp := e.TranscriptPath(proj, sess)

	// The body cites the ASK (task-body's concern, grounded against the user pool);
	// the FRONTMATTER carries the DELIVERY evidence task-review judges.
	body := "Migrated the auth module. The user asked to " + cite("migrate the auth module", tp, 1) + "."
	obs := []string{tp + ":" + itoa(line)} // absolute jsonl, the tool_result line
	art := []string{artifactRel + ":3-5"}  // repo-relative tree file:lines

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, taskWithEvidence("in_review", "P1", body, obs, art)),
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

// TestReview_UnsubstantiatedBlocksAtStop: an in_review task whose delivery evidence
// RESOLVES (so it reaches the judge) but which the review judge REJECTS — the
// evidence does not show the claimed thing — is blocked at Stop, and the rejection
// reaches the agent. task-body is disabled so the single stubbed verdict (pass:false)
// is task-review's alone. This is the "delivered evidence does not substantiate the
// claim" case the judge exists for.
func TestReview_UnsubstantiatedBlocksAtStop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.DisablePluginGuardrail(proj, pluginName+"/file-guard/task-body-is-human-authored")
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "REVIEW REJECTED: observations[0] shows the test command but its output does not mention the auth token work the task claims"}`)

	sess := "s-review-reject"
	artifactRel := "src/auth.go"
	line := prepareDelivery(t, e, proj, sess, "PASS", artifactRel)
	tp := e.TranscriptPath(proj, sess)

	body := "Migrated the auth module."
	obs := []string{tp + ":" + itoa(line)}
	art := []string{artifactRel + ":3-5"}

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, taskWithEvidence("in_review", "P1", body, obs, art)),
	))
	_ = res

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("an unsubstantiated in_review task was not blocked at Stop:\n%s", res.Output)
	}
	joined := strings.Join(blocks, "\n")
	if !containsStr(joined, "does not mention the auth token work") {
		t.Errorf("the review judge's rejection reasoning did not reach the agent at Stop:\n%s", joined)
	}
}

// TestReview_NotInReviewSkipsTheJudge: a task that is NOT in_review (here to_do)
// reaches the Stop after-check — its write lands (task-evidence-resolves requires
// evidence only for in_review, so an evidence-less to_do is permitted at Pre) — but
// the review JUDGE is NEVER invoked. expand-evidence.sh reads the status and emits
// `{"skip": true}`, which makes the judge check ABSTAIN: no model call, no verdict.
// The judge and the pre-flight are two SEPARATE checks, and a passing pre-flight does
// not end the chain, so without the skip the judge would run a full model call to
// "review" a task not up for review. Proven by the CAPTURING judge writing NO prompt.
//
// (The turn is still blocked at Stop — by no-unfinished-work-at-turn-end, because a
// to_do task is open work — but that block is a different guardrail's, and crucially
// it is NOT the review judge: the review simply abstained.)
func TestReview_NotInReviewSkipsTheJudge(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	// task-body-is-human-authored guards this same path at Pre, and its stage-1 script
	// requires a grounding `[quote](jsonl)` link in the body — the bare to_do body below
	// has none, so with task-body enabled the write would be REFUSED at Pre and never
	// reach Stop (a false failure unrelated to the review). It also has a stage-2 judge
	// sharing the single capturing stub, which would capture a prompt and mask what we
	// measure. Disabling it removes both: the write lands, and the ONLY judge that can
	// capture a prompt is task-review's — so an empty capture proves the REVIEW judge
	// specifically did not run. task-evidence-resolves stays enabled (deterministic, no
	// judge); it requires no evidence for a non-in_review task, so the to_do write lands.
	e.DisablePluginGuardrail(proj, pluginName+"/file-guard/task-body-is-human-authored")
	// A capturing judge: it records its prompt to a file, so a non-empty prompt is proof
	// the model was invoked. Stubbed PASS so that IF it wrongly ran it would not itself
	// refuse — the only signal read is whether a prompt was captured.
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	// to_do — not up for review — with NO delivery evidence. (No body citation needed:
	// task-body is disabled, and task-evidence-resolves does not require evidence off an
	// in_review task, so a bare to_do write lands and reaches the Stop review.)
	body := "Will migrate the auth module later."
	res := e.Run(proj, "s-review-todo", authPrompt, Turns("done",
		Write("w1", taskPath, taskWithEvidence("to_do", "P1", body, nil, nil)),
	))

	if res.Refused() {
		t.Fatalf("a to_do task write was refused at Pre — it should land and reach Stop:\n%s", res.Output)
	}
	if !e.Exists(proj, taskPath) {
		t.Fatalf("the to_do task did not land on disk, so it never reached the Stop review")
	}
	// THE POINT: the review judge did not run for a non-in_review task.
	if prompt := e.JudgePrompt(proj, "judge-prompt.txt"); prompt != "" {
		t.Fatalf("the review judge WAS invoked for a to_do task (%d-byte prompt) — the skip did not abstain the check:\n%s", len(prompt), prompt)
	}
}

// TestReview_ObservationNotAToolResultRefusedByPreflight: an in_review task whose
// observation cites a transcript line that is NOT a tool_result (here the user's own
// prompt line) is refused deterministically — task-review's pre-flight refuses when
// reached, without a model, because there is nothing showing the work happened. Here
// task-evidence (also enabled) refuses it at Pre first, so the write never lands; the
// point proven is that an observation pointing at the agent's prose (or the user's
// ask) rather than a real result cannot slip through to an approval. The judge stub
// is PASS to show the DETERMINISTIC layer is what refuses.
func TestReview_ObservationNotAToolResultRefusedByPreflight(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-review-notresult"
	artifactRel := "src/auth.go"
	_ = prepareDelivery(t, e, proj, sess, "PASS", artifactRel)
	tp := e.TranscriptPath(proj, sess)

	// A grounded body citation so task-body passes (judge stubbed PASS) and the
	// refusal is specifically the observation check. The observation cites line 1 —
	// the user's PROMPT — which is not a tool_result.
	body := "Migrated the auth module. The user asked to " + cite("migrate the auth module", tp, 1) + "."
	obs := []string{tp + ":1"}
	art := []string{artifactRel + ":3-5"}

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, taskWithEvidence("in_review", "P1", body, obs, art)),
	))

	if !res.Refused() {
		t.Fatalf("an in_review task whose observation is not a tool_result was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("an in_review task with a non-tool_result observation landed on disk")
	}
	if !res.Saw("is NOT a tool_result") {
		t.Errorf("the refusal was not the not-a-tool_result reason:\n%s", res.Output)
	}
}

// reviewGateTaskPath and its gate, a group distinct from taskPath's so these
// tests never collide with another test file's task.
const (
	reviewGateTaskPath = "memories/tasks/web/ship-it/TASK.md"
	reviewGateShPath   = "memories/tasks/web/ship-it/gates/repo-is-public.sh"
	reviewGateMdPath   = "memories/tasks/web/ship-it/gates/launch-video-exists.md"
)

// TestReview_ShGateNoLongerHoldingBlocksAtStop: a gates/*.sh that PASSED at
// the start (so the task legitimately reached in_progress) is rewritten to
// FAIL before the task is claimed in_review -- proving task-review re-holds
// the SAME gate at the completion claim, not just task-gates-hold at the
// start. The delivery evidence itself is real and would otherwise pass, so
// the gate is isolated as what refuses.
func TestReview_ShGateNoLongerHoldingBlocksAtStop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-review-gate-regressed"
	tp := e.TranscriptPath(proj, sess)
	body := "Migrated the auth module. The user asked to " + cite("migrate the auth module", tp, 1) + "."
	backlogDoc := "---\nstatus: backlog\npriority: P1\n---\n\n" + body + "\n"

	// Land the task and a PASSING gate together, then move to_do -- the gate
	// held at the start, so this transition is legitimate.
	res0 := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w0", reviewGateTaskPath, backlogDoc),
		Write("w1", reviewGateShPath, passingGate),
	))
	if res0.Refused() {
		t.Fatalf("landing the task and its passing gate was refused (setup broken):\n%s", res0.Output)
	}
	toDoDoc := "---\nstatus: to_do\npriority: P1\n---\n\n" + body + "\n"
	res1 := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w2", reviewGateTaskPath, toDoDoc),
	))
	if res1.Refused() {
		t.Fatalf("moving to to_do with a passing gate was refused (setup broken):\n%s", res1.Output)
	}

	// The gate REGRESSES -- rewritten to fail, after the task has already
	// started. This write itself must be admitted (the judge is stubbed
	// PASS), so a later attempt to claim in_review is what this test targets.
	res2 := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w3", reviewGateShPath, failingGate),
	))
	if res2.Refused() {
		t.Fatalf("rewriting the gate to fail was itself refused (setup broken):\n%s", res2.Output)
	}

	artifactRel := "src/auth.go"
	line := prepareDelivery(t, e, proj, sess, "PASS", artifactRel)
	obs := []string{tp + ":" + itoa(line)}
	art := []string{artifactRel + ":3-5"}
	inReviewDoc := taskWithEvidence("in_review", "P1", body, obs, art)

	res3 := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w4", reviewGateTaskPath, inReviewDoc),
	))
	if res3.Refused() {
		t.Fatalf("the in_review write itself was refused at Pre (setup broken):\n%s", res3.Output)
	}

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a task claiming in_review with a gate that no longer holds was not blocked at Stop:\n%s", res3.Output)
	}
	joined := strings.Join(blocks, "\n")
	if !containsStr(joined, "GATES NO LONGER HOLD") {
		t.Errorf("the Stop block was not the gates-no-longer-hold reason:\n%s", joined)
	}
}

// TestReview_MdGateJudgeRejectionBlocksAtStop: a gates/*.md judgment gate
// exists, and task-review's OWN judge call (the same one that weighs the
// delivery evidence) rejects it -- proving a judgment gate is folded into the
// review judge rather than needing a separate model call. The stub applies to
// every judge in the run, so this isolates task-review's judge the same way
// TestReview_SubstantiatedPermits's siblings do: task-body is disabled and
// the delivery evidence is real, leaving the gate as the only thing that
// could make the (single, stubbed) verdict a FAIL.
func TestReview_MdGateJudgeRejectionBlocksAtStop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.DisablePluginGuardrail(proj, pluginName+"/file-guard/task-body-is-human-authored")

	// SETUP needs every OTHER judge in the run (task-gate-is-grounded's on the
	// gate write, task-gates-hold's on the to_do transition) to PASS; only
	// the FINAL in_review write's stub is flipped to FAIL, below.
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-review-gate-judge-reject"
	backlogDoc := "---\nstatus: backlog\npriority: P1\n---\n\nShip the launch page.\n"

	res0 := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w0", reviewGateTaskPath, backlogDoc),
		Write("w1", reviewGateMdPath, "The launch video exists and shows a working demo.\n"),
	))
	if res0.Refused() {
		t.Fatalf("landing the task and its judgment gate was refused (setup broken):\n%s", res0.Output)
	}
	toDoDoc := "---\nstatus: to_do\npriority: P1\n---\n\nShip the launch page.\n"
	res1 := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w2", reviewGateTaskPath, toDoDoc),
	))
	if res1.Refused() {
		t.Fatalf("moving to to_do was refused (setup broken):\n%s", res1.Output)
	}

	artifactRel := "src/launch.go"
	tp := e.TranscriptPath(proj, sess)
	line := prepareDelivery(t, e, proj, sess, "PASS", artifactRel)
	obs := []string{tp + ":" + itoa(line)}
	art := []string{artifactRel + ":3-5"}
	inReviewDoc := taskWithEvidence("in_review", "P1", "Shipped the launch page.", obs, art)

	// The single stubbed verdict for this run's judges is FAIL -- since
	// task-body is disabled, the only judge left in play is task-review's,
	// which now also weighs the gates/*.md file gathered by expand-evidence.sh.
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "REVIEW REJECTED: the launch video gate does not hold -- no evidence the video exists."}`)

	res2 := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w3", reviewGateTaskPath, inReviewDoc),
	))
	if res2.Refused() {
		t.Fatalf("the in_review write itself was refused at Pre (setup broken):\n%s", res2.Output)
	}

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("an in_review task whose judgment gate the judge rejects was not blocked at Stop:\n%s", res2.Output)
	}
	joined := strings.Join(blocks, "\n")
	if !containsStr(joined, "the launch video gate does not hold") {
		t.Errorf("the judge's rejection reasoning did not reach the agent at Stop:\n%s", joined)
	}
}
