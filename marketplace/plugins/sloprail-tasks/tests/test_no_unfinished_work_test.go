package e2e

import "testing"

// no-unfinished-work-at-turn-end is a GATE on Stop (the migration of the old-format
// TurnEnd hook). It carries no matcher — Stop has no fields — so it walks the task
// tree under $SR_WORKSPACE and refuses the turn if any
// memories/tasks/<cat>/<name>/TASK.md is `to_do` or `in_progress`. A gate BLOCKS,
// which is what a turn-boundary checkpoint needs; backlog / blocked / in_review are
// allowed to rest.
//
// The gate itself is DETERMINISTIC (no judge). But the task WRITE that sets up each
// test fires the PreFileWrite gates (task-evidence, task-body) at Pre, and
// task-body has a judge — so a passing judge stub (pass:true) is installed to let
// the write land, isolating the gate's own Stop decision. The write is an sr-file
// call citing the user's words, resolved against the seeded transcript for real.
//
// These prove: a turn ending with an open (to_do) task is BLOCKED at Stop, naming
// the task and telling the agent how to finish it (the cited in_review form); and
// a turn whose task is at a resting status (in_review) PERMITS.

// TestUnfinished_OpenTaskBlocksAtStop: a turn that leaves a to_do task open is
// blocked at Stop, and the refusal names the open task and its status. The write
// lands at Pre (cited body + judge PASS), so the to_do task is on disk when the
// gate walks the tree at Stop.
func TestUnfinished_OpenTaskBlocksAtStop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-unfinished-open"
	res := e.Run(proj, sess, authPrompt, Turns("done",
		srWrite("b1", taskPath, task("to_do", "P1", askBody), citeUser(askQuote)),
	).ThenCommit("File the task", CitesUser(askQuote)))

	if res.Refused() {
		t.Fatalf("the to_do task write itself was refused at Pre (setup broken):\n%s", res.Output)
	}
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a turn with an open to_do task was not blocked at Stop:\n%s", res.Output)
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	if !containsStr(joined, "A turn cannot end with open work") {
		t.Errorf("the Stop block was not the open-work refusal:\n%s", joined)
	}
	// It names the open task and its status, so the message is a work queue.
	if !containsStr(joined, taskPath) || !containsStr(joined, "status: to_do") {
		t.Errorf("the refusal did not name the open task and its status:\n%s", joined)
	}
	// The way to finish it is the cited transition, not a link pasted into the file.
	if !containsStr(joined, "--cite:tool_result") || containsStr(joined, "jsonl") {
		t.Errorf("the refusal does not teach the cited in_review transition:\n%s", joined)
	}
}

// TestUnfinished_AllRestingPermits: a turn whose only task is at a RESTING status
// (in_review — the agent is done, cited the tool output proving it, and the review
// guard owns it now) permits at Stop. The in_review write carries REAL delivery
// evidence (tool output from a Bash run in the same turn, cited on the write, and a
// repo-relative artifact), so task-evidence lets it land and task-review's judge
// (stubbed PASS) accepts, leaving the gate as the only thing that could block the
// Stop — and it must not, because in_review is allowed to rest. The control for
// the block test above.
func TestUnfinished_AllRestingPermits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-unfinished-resting"
	doc := taskWithArtifacts("in_review", "P1", askBody, []string{deliveredLines})
	res := e.Run(proj, sess, authPrompt, Turns("done", then(deliveryTurns(deliveredArtifact),
		srWrite("b1", taskPath, doc, citeUser(askQuote), citeTool(proofMarker)),
	)...).ThenCommit("Deliver the task", CitesUser(askQuote), CitesTool(proofMarker)))

	if res.Refused() {
		t.Fatalf("the in_review task write was refused at Pre (setup broken):\n%s", res.Output)
	}
	// The gate's own refusal must not appear at Stop — in_review rests.
	for _, b := range e.BlockingErrorsFrom(proj, sess, "Stop") {
		if containsStr(b, "A turn cannot end with open work") {
			t.Fatalf("the gate blocked a turn whose task was in_review (a resting status):\n%s", b)
		}
	}
}
