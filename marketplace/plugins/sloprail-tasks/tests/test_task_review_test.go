package e2e

import (
	"strings"
	"testing"
)

// task-review is an AFTER-CHECK file-guard over
// memories/tasks/<cat>/<name>/TASK.md — it fires at the Post/Stop after-check on the
// SETTLED file, never at Pre. It reviews a task written into `in_review`, and it
// proves DELIVERY, not the ask: a SCRIPT pre-flight (only in_review; the claim
// must carry cited tool output and resolving artifacts), then a PREPARE + JUDGE
// that decides whether that delivered evidence SUBSTANTIATES the claim. The judge
// FAILING refuses the turn (REVIEW REJECTED); the task stays in_review, and because
// a Post refusal does not advance the read mark it re-fires next cycle until fixed.
//
// # The evidence
//
//   - CITED TOOL RESULTS — the write that moved the task into in_review carried
//     `sr-file … --cite:tool_result '<exact output>'`; the Post event at Stop
//     carries those citations, and the prepare hands the judge each quote AND the
//     full tool result at its line.
//   - ARTIFACTS — `artifacts: ["src/auth.go:3-5"]` in the frontmatter, expanded to
//     the cited tree lines.
//
// The task file holds no transcript path. A passing case therefore runs real work
// in the same run (deliveryTurns: the artifact lands, a Bash run prints
// proofOutput) and then claims it with a cited sr-file write.
//
// # Why some tests disable the sibling body guard
//
// task-review shares its path with task-body-is-human-authored, which has its OWN
// judge. The harness's single judge stub gives ONE verdict to every judge, so a test
// needing task-review's judge to FAIL while task-body PASSES cannot express that with
// one stub. Where that conflict arises the test DISABLES task-body so the one stubbed
// verdict is task-review's alone. task-evidence-resolves is deterministic (no judge)
// and stays enabled: its Pre check passes for a cited transition, so the in_review
// write lands and reaches task-review at Stop.

// TestReview_SubstantiatedPermits: an in_review task whose claim cites the REAL
// tool output of the work and names the REAL produced file, and that the review
// judge accepts, permits — the review runs at Stop and does NOT block. The reviewer
// is handed the FULL tool result, not only the quoted words: proofOutput's
// unquoted part reaches the review prompt.
func TestReview_SubstantiatedPermits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	sess := "s-review-ok"
	doc := taskWithArtifacts("in_review", "P1", askBody, []string{deliveredLines})
	res := e.Run(proj, sess, authPrompt, Turns("done", then(deliveryTurns(deliveredArtifact),
		srWrite("b1", taskPath, doc, citeUser(askQuote), citeTool(proofMarker)),
	)...))

	if res.Refused() {
		t.Fatalf("a substantiated in_review task write was refused at Pre:\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("a substantiated in_review task was blocked at Stop:\n%v", blocks)
	}
	if !e.Exists(proj, taskPath) {
		t.Errorf("the in_review task did not land on disk")
	}
	// The review judge runs last at Stop (task-review sorts after task-body), so the
	// captured prompt is the reviewer's.
	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if !containsStr(prompt, "<cited_results>") || !containsStr(prompt, "quoted: "+proofMarker) {
		t.Fatalf("the reviewer was not handed the cited tool result:\n%s", prompt)
	}
	if !containsStr(prompt, "ok  sloprail/auth") {
		t.Errorf("the reviewer saw only the quote, not the full tool output it came from:\n%s", prompt)
	}
	// And the call that printed it — here an echo, which the reviewer must see.
	if !containsStr(prompt, "produced by: Bash: echo") {
		t.Errorf("the reviewer was not told which call produced the cited output:\n%s", prompt)
	}
	if !containsStr(prompt, "migrated to the new token format") {
		t.Errorf("the reviewer was not handed the artifact's cited lines:\n%s", prompt)
	}
}

// TestReview_UnsubstantiatedBlocksAtStop: an in_review task whose evidence is there
// (so it reaches the judge) but which the review judge REJECTS is blocked at Stop,
// and the rejection reaches the agent. task-body is disabled so the single stubbed
// verdict (pass:false) is task-review's alone.
func TestReview_UnsubstantiatedBlocksAtStop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.DisablePluginGuardrail(proj, pluginName+"/file-guard/task-body-is-human-authored", pluginName+"/gate/task-body-is-human-authored")
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "REVIEW REJECTED: cited_results[0] shows a test run but its output does not mention the auth token work the task claims"}`)

	sess := "s-review-reject"
	doc := taskWithArtifacts("in_review", "P1", "Migrated the auth module.", []string{deliveredLines})
	res := e.Run(proj, sess, authPrompt, Turns("done", then(deliveryTurns(deliveredArtifact),
		srWrite("b1", taskPath, doc, citeTool(proofMarker)),
	)...))
	if res.Refused() {
		t.Fatalf("the in_review write itself was refused at Pre (setup broken):\n%s", res.Output)
	}

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
// reaches the Stop after-check but the review JUDGE is NEVER invoked:
// expand-evidence.sh reads the status and emits `{"skip": true}`, which makes the
// judge check ABSTAIN. Proven by the CAPTURING judge writing NO prompt.
//
// (The turn is still blocked at Stop — by no-unfinished-work-at-turn-end, because a
// to_do task is open work — but that block is a different guardrail's.)
func TestReview_NotInReviewSkipsTheJudge(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	// task-body-is-human-authored guards this same path: the uncited Write below
	// would be refused at Pre, and its judge shares the single capturing stub.
	// Disabling it leaves task-review's judge as the ONLY one that could capture a
	// prompt, so an empty capture proves the REVIEW judge specifically did not run.
	e.DisablePluginGuardrail(proj, pluginName+"/file-guard/task-body-is-human-authored", pluginName+"/gate/task-body-is-human-authored")
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-review-todo", authPrompt, Turns("done",
		Write("w1", taskPath, taskWithArtifacts("to_do", "P1", "Will migrate the auth module later.", nil)),
	))

	if res.Refused() {
		t.Fatalf("a to_do task write was refused at Pre — it should land and reach Stop:\n%s", res.Output)
	}
	if !e.Exists(proj, taskPath) {
		t.Fatalf("the to_do task did not land on disk, so it never reached the Stop review")
	}
	if prompt := e.JudgePrompt(proj, "judge-prompt.txt"); prompt != "" {
		t.Fatalf("the review judge WAS invoked for a to_do task (%d-byte prompt) — the skip did not abstain the check:\n%s", len(prompt), prompt)
	}
}

// TestReview_EditedClaimWithoutProofRefusedAtStop: a task that was ALREADY
// in_review when the session began (committed, its artifact in the tree) is edited
// in this session WITHOUT a citation — a priority change with the Write tool. That
// is not a transition, so task-evidence permits it; but no change to it this
// session cited tool output, so the claim reaching Stop has none on record and the
// reviewer has nothing to weigh. task-review's declared requirement (a tool_result
// citation `when` the task is in_review) refuses it deterministically, before any
// model call (the stub is PASS to show the deterministic layer is what refuses),
// naming how to cite the proof.
func TestReview_EditedClaimWithoutProofRefusedAtStop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	e.WriteFile(proj, deliveredArtifact, "package auth\n\n// migrated\nfunc Migrate() error {\n\treturn nil\n}\n")
	seedBaselineTask(t, e, proj, taskPath, taskWithArtifacts("in_review", "P1", askBody, []string{deliveredLines}))

	sess := "s-review-no-proof"
	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, taskWithArtifacts("in_review", "P2", askBody, []string{deliveredLines})),
	))
	if res.Refused() {
		t.Fatalf("a frontmatter-only edit of an in_review task was refused at Pre (setup broken):\n%s", res.Output)
	}

	joined := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	if !containsStr(joined, "was changed without citing a tool's output from this session (--cite:tool_result)") {
		t.Fatalf("an in_review claim with no cited tool output was not refused at Stop:\n%s", joined)
	}
	if !containsStr(joined, "--cite:tool_result") {
		t.Errorf("the refusal does not say how to cite the proof:\n%s", joined)
	}
}

// TestReview_UncitedEditAfterCitedTransitionIsRefused: in ONE session the task
// is moved into in_review with cited tool output, then edited again with the
// plain Write tool (a priority change). A citation grounds only the change it
// rode on: the transition's proof does not ground the later uncited edit of an
// in_review task, so Stop refuses it and says how to cite.
func TestReview_UncitedEditAfterCitedTransitionIsRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-review-uncited-after"
	doc := taskWithArtifacts("in_review", "P1", askBody, []string{deliveredLines})
	res := e.Run(proj, sess, authPrompt, Turns("done", then(deliveryTurns(deliveredArtifact),
		srWrite("b1", taskPath, doc, citeUser(askQuote), citeTool(proofMarker)),
		Write("w2", taskPath, taskWithArtifacts("in_review", "P2", askBody, []string{deliveredLines})),
	)...))
	if res.Refused() {
		t.Fatalf("the cited transition or the uncited priority edit was refused at Pre:\n%s", res.Output)
	}
	if got := readFile(t, proj, taskPath); !strings.Contains(got, "priority: P2") {
		t.Fatalf("the uncited edit did not land:\n%s", got)
	}
	joined := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	if !containsStr(joined, "was changed without a citation") || !containsStr(joined, "--cite:tool_result") {
		t.Fatalf("an uncited edit of an in_review task passed at Stop on an earlier change's citation:\n%s", joined)
	}
}

// TestReview_CitedEditAfterCitedTransitionKeepsEvidence: the same flow with the
// later edit made the grounded way. task-review requires tool output for every
// change to an in_review task (TestReview_EditedClaimWithoutProofRefusedAtStop:
// even a priority-only edit), and a change counts only in the pool it was cited
// in — so the priority edit cites tool output of its own (a second run),
// beside the user's words. The claim reaching Stop carries BOTH changes' proof,
// the pre-flight passes and the reviewer is handed each.
func TestReview_CitedEditAfterCitedTransitionKeepsEvidence(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	sess := "s-review-accumulate"
	doc := taskWithArtifacts("in_review", "P1", askBody, []string{deliveredLines})
	res := e.Run(proj, sess, authPrompt, Turns("done", then(deliveryTurns(deliveredArtifact),
		srWrite("b1", taskPath, doc, citeUser(askQuote), citeTool(proofMarker)),
		Bash("p2", "echo 'PROOF-TWO-7715 re-ran the suite, still green'"),
		srEdit("b2", taskPath, "priority: P1", "priority: P2", citeUser(askQuote), citeTool("PROOF-TWO-7715")),
	)...))
	if res.Refused() {
		t.Fatalf("the cited transition or the cited priority edit was refused at Pre:\n%s", res.Output)
	}
	if got := readFile(t, proj, taskPath); !strings.Contains(got, "priority: P2") {
		t.Fatalf("the cited edit did not land:\n%s", got)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("an in_review claim whose transition cited tool output was blocked at Stop after a cited edit:\n%v", blocks)
	}
	// Cited changes accumulate: the reviewer is handed the transition's proof AND
	// the later edit's, each with its own tool output.
	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	for _, marker := range []string{proofMarker, "PROOF-TWO-7715"} {
		if !containsStr(prompt, "quoted: "+marker) {
			t.Errorf("the reviewer was not handed the tool output cited as %q:\n%s", marker, prompt)
		}
	}
}

// reviewGateTaskPath and its gate, a group distinct from taskPath's so these
// tests never collide with another test file's task.
const (
	reviewGateTaskPath = "memories/tasks/web/ship-it/TASK.md"
	reviewGateShPath   = "memories/tasks/web/ship-it/gates/repo-is-public.sh"
	reviewGateMdPath   = "memories/tasks/web/ship-it/gates/launch-video-exists.md"
)

// toReviewEdit is the cited edit that claims the work: to_do -> in_review, naming
// the artifact, citing the delivery run's output.
func toReviewEdit(id, path string) Turn {
	return srEdit(id, path, "status: to_do",
		"status: in_review\nartifacts: [\""+deliveredLines+"\"]",
		citeTool(proofMarker))
}

// TestReview_ShGateNoLongerHoldingBlocksAtStop: a gates/*.sh that PASSED at
// the start (so the task legitimately reached to_do) is rewritten to FAIL before
// the task is claimed in_review -- proving task-review re-holds the SAME gate at
// the completion claim, not just task-gates-hold at the start. The delivery
// evidence itself is real, so the gate is isolated as what refuses.
func TestReview_ShGateNoLongerHoldingBlocksAtStop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-review-gate-regressed"

	// Land the task (cited) and a PASSING gate together, then move to_do -- the
	// gate held at the start, so this transition is legitimate. The later runs
	// change only the frontmatter or the gate, so they cite no user words.
	res0 := e.Run(proj, sess, authPrompt, Turns("done",
		srWrite("b0", reviewGateTaskPath, task("backlog", "P1", askBody), citeUser(askQuote)),
		Write("w1", reviewGateShPath, passingGate),
	))
	if res0.Refused() {
		t.Fatalf("landing the task and its passing gate was refused (setup broken):\n%s", res0.Output)
	}
	res1 := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w2", reviewGateTaskPath, task("to_do", "P1", askBody)),
	))
	if res1.Refused() {
		t.Fatalf("moving to to_do with a passing gate was refused (setup broken):\n%s", res1.Output)
	}

	// The gate REGRESSES -- rewritten to fail, after the task has already started.
	res2 := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w3", reviewGateShPath, failingGate),
	))
	if res2.Refused() {
		t.Fatalf("rewriting the gate to fail was itself refused (setup broken):\n%s", res2.Output)
	}

	res3 := e.Run(proj, sess, authPrompt, Turns("done", then(deliveryTurns(deliveredArtifact),
		toReviewEdit("b4", reviewGateTaskPath),
	)...))
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
// review judge rather than needing a separate model call. task-body is disabled
// and the delivery evidence is real, leaving the gate as the only thing that
// could make the (single, stubbed) verdict a FAIL.
func TestReview_MdGateJudgeRejectionBlocksAtStop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.DisablePluginGuardrail(proj, pluginName+"/file-guard/task-body-is-human-authored", pluginName+"/gate/task-body-is-human-authored")

	// SETUP needs every OTHER judge in the run (task-gate-is-grounded's on the
	// gate write, task-gates-hold's on the to_do transition) to PASS; only
	// the FINAL in_review write's stub is flipped to FAIL, below.
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-review-gate-judge-reject"
	res0 := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w0", reviewGateTaskPath, task("backlog", "P1", "Ship the launch page.")),
		Write("w1", reviewGateMdPath, "The launch video exists and shows a working demo.\n"),
	))
	if res0.Refused() {
		t.Fatalf("landing the task and its judgment gate was refused (setup broken):\n%s", res0.Output)
	}
	res1 := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w2", reviewGateTaskPath, task("to_do", "P1", "Ship the launch page.")),
	))
	if res1.Refused() {
		t.Fatalf("moving to to_do was refused (setup broken):\n%s", res1.Output)
	}

	// The single stubbed verdict for this run's judges is FAIL -- since task-body
	// is disabled, the only judge left in play is task-review's, which now also
	// weighs the gates/*.md file gathered by expand-evidence.sh.
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "REVIEW REJECTED: the launch video gate does not hold -- no evidence the video exists."}`)

	res2 := e.Run(proj, sess, authPrompt, Turns("done", then(deliveryTurns(deliveredArtifact),
		toReviewEdit("b3", reviewGateTaskPath),
	)...))
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
