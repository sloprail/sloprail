package e2e

import (
	"strings"
	"testing"
)

// task-evidence-resolves is a PreFileWrite gate and a file-guard (Stop, over the committed changeset) over
// memories/tasks/<cat>/<name>/TASK.md with one deterministic SCRIPT check:
//
//   - the frontmatter satisfies task.cue (closed; no `done`, no `observations`);
//   - every artifact `<repo-relative-file>:<ranges>` resolves in the tree, and an
//     in_review task names at least one;
//   - a write that moves the task INTO in_review carries at least one citation
//     whose sourceTypes include tool_result — `sr-file edit … --cite:tool_result
//     '<exact output>'` — the proof the work happened, riding on the claim itself.
//     The file holds no transcript path of any kind.
//
// The PreFileWrite gate refuses a not-fine write at PRE-tool, before it lands; the file-guard
// of the same name re-checks the committed task at Stop, the tool-output proof taken from
// the commit's Sloprail-Cites-Tool trailer.
//
// This is the DETERMINISTIC half of the review lifecycle: it answers only "is the
// evidence there", the gate task-review's judge depends on. task-body-is-human-
// authored guards the same path and has a judge, so a passing judge stub is
// installed wherever a write must land, isolating what task-evidence does.
//
// Each delivery test runs the work in the SAME run as the claim: deliveryTurns
// writes the artifact and prints proofOutput from a real Bash run, so the
// tool_result is on the transcript before the in_review write cites it.

// deliveredArtifact is the file deliveryTurns writes, and the lines of it an
// artifact cites (the migrated function).
const (
	deliveredArtifact = "src/auth.go"
	deliveredLines    = deliveredArtifact + ":3-5"
)

// TestEvidence_CitedTransitionPermits: a task created straight into in_review,
// naming a real artifact and citing the real tool output that proves the work
// (plus the user's words for its body), permits and lands. The control for every
// refusal below.
func TestEvidence_CitedTransitionPermits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	doc := taskWithArtifacts("in_review", "P1", askBody, []string{deliveredLines})
	res := e.Run(proj, "s-evidence-ok", authPrompt, Turns("done", then(deliveryTurns(deliveredArtifact),
		srWrite("b1", taskPath, doc, citeUser(askQuote), citeTool(proofMarker)),
	)...).ThenCommit("Deliver the task", CitesUser(askQuote), CitesTool(proofMarker)))

	if res.Refused() {
		t.Fatalf("an in_review task citing real tool output was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, taskPath) {
		t.Errorf("an admitted task write did not land on disk")
	}
	if got := readFile(t, proj, taskPath); containsStr(got, ".jsonl") {
		t.Errorf("the task file carries a transcript path; it should hold derived text only:\n%s", got)
	}
	if blocks := stopBlocks(e, proj, "s-evidence-ok"); containsStr(blocks, "EVIDENCE") || containsStr(blocks, "TASK FRONTMATTER") {
		t.Errorf("the committed in_review task was refused at Stop by the evidence check:\n%s", blocks)
	}
}

// TestEvidence_TransitionWithoutToolCitationRefused: an in_progress task (created
// with a user citation) is moved to in_review by an sr-file edit that cites only
// the USER's words — the ask, not proof of delivery. Grounding a delivery claim in
// the request proves only that the work was asked for; the transition is refused,
// the refusal names the --cite:tool_result form, and the status stays in_progress.
func TestEvidence_TransitionWithoutToolCitationRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	doc := taskWithArtifacts("in_progress", "P1", askBody, []string{deliveredLines})
	res := e.Run(proj, "s-evidence-no-proof", authPrompt, Turns("done", then(deliveryTurns(deliveredArtifact),
		srWrite("b1", taskPath, doc, citeUser(askQuote)),
		srEdit("b2", taskPath, "status: in_progress", "status: in_review", citeUser(askQuote)),
	)...))

	if !res.Refused() {
		t.Fatalf("a move to in_review citing no tool output was not refused:\n%s", res.Output)
	}
	if !res.Saw("a tool's output from this session") || !res.Saw("--cite:tool_result") {
		t.Errorf("the refusal did not name the missing proof and how to cite it:\n%s", res.Output)
	}
	if got := readFile(t, proj, taskPath); !strings.Contains(got, "status: in_progress") {
		t.Errorf("the refused transition reached the file:\n%s", got)
	}
}

// TestEvidence_UncitedToolEditTransitionRefused: the same transition made with the
// plain Write tool — which cannot carry a citation at all — is refused too. The
// task was committed in_progress, so this write changes only the frontmatter and
// task-body has nothing to say: the refusal is task-evidence's declared
// requirement alone.
func TestEvidence_UncitedToolEditTransitionRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	e.WriteFile(proj, deliveredArtifact, "package auth\n\n// migrated\nfunc Migrate() error {\n\treturn nil\n}\n")
	seedBaselineTask(t, e, proj, taskPath, taskWithArtifacts("in_progress", "P1", askBody, []string{deliveredLines}))

	res := e.Run(proj, "s-evidence-write-tool", authPrompt, Turns("done",
		Write("w1", taskPath, taskWithArtifacts("in_review", "P1", askBody, []string{deliveredLines})),
	))

	if !res.Refused() {
		t.Fatalf("an uncited move to in_review was not refused:\n%s", res.Output)
	}
	if !res.Saw("must cite a tool's output from this session (--cite:tool_result)") {
		t.Errorf("the refusal was not the missing-proof requirement:\n%s", res.Output)
	}
	// The rule's own hint, from its `when` script: what proof is, and the edit.
	if !res.Saw("Run what proves the work") || !res.Saw("--old-string 'status: in_progress' --new-string 'status: in_review'") {
		t.Errorf("the refusal does not carry the rule's hint:\n%s", res.Output)
	}
}

// TestEvidence_AnswerEnvelopeIsNotToolOutput is the adversarial case: an
// AskUserQuestion answer re-enters the transcript as a tool_result BLOCK, so a
// naive "is it a tool_result" check would take the USER's own "yes, proceed" as
// delivery proof. Cited as --cite:tool_result it resolves to nothing — the answer
// envelope is the user's words, never a produced result — so the transition does
// not happen. (sr-file refuses to write on an unresolved quote, and the pre-tool
// check refuses a write with no tool output cited; either way nothing lands.)
func TestEvidence_AnswerEnvelopeIsNotToolOutput(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-evidence-answerenv"
	doc := taskWithArtifacts("in_progress", "P1", askBody, []string{deliveredLines})
	// Run 1: the artifact and the task land, then the user answers a question. An
	// answer is a user record, which ends the run, so it comes last.
	e.Run(proj, sess, authPrompt, Turns("done",
		Write("wf", deliveredArtifact, "package auth\n\n// migrated\nfunc Migrate() error {\n\treturn nil\n}\n"),
		srWrite("b1", taskPath, doc, citeUser(askQuote)),
		AnswerQuestion("q1", [2]string{"proceed with the migration?", "yes, proceed"}),
	))
	if !e.Exists(proj, taskPath) {
		t.Fatalf("run 1 did not land the in_progress task (setup broken)")
	}

	res := e.Run(proj, sess, "Wrap it up.", Turns("done",
		srEdit("b2", taskPath, "status: in_progress", "status: in_review", citeTool("yes, proceed")),
	))

	if got := readFile(t, proj, taskPath); !strings.Contains(got, "status: in_progress") {
		t.Fatalf("the user's own answer passed as delivery proof — the task reached in_review:\n%s\n%s", got, res.Output)
	}
}

// TestEvidence_ArtifactMissingFromTreeRefused: an artifact citing a repo-relative
// file that is not in the tree is refused — the file-citation check resolves it
// under the repo root and finds nothing. The write never lands.
func TestEvidence_ArtifactMissingFromTreeRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	doc := taskWithArtifacts("in_review", "P1", askBody, []string{"src/does-not-exist.go:1-3"})
	res := e.Run(proj, "s-evidence-noart", authPrompt, Turns("done", then(deliveryTurns(deliveredArtifact),
		srWrite("b1", taskPath, doc, citeUser(askQuote), citeTool(proofMarker)),
	)...))

	if !res.Refused() {
		t.Fatalf("an artifact absent from the tree was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("the gate let a missing-artifact task land on disk")
	}
	if !res.Saw("not in the tree") {
		t.Errorf("the refusal was not the missing-artifact reason:\n%s", res.Output)
	}
}

// TestEvidence_AbsoluteArtifactRefusedBySchema: an artifact with an ABSOLUTE path
// would not survive a different checkout. task.cue's _artifact regex refuses it at
// the schema check, before resolution.
func TestEvidence_AbsoluteArtifactRefusedBySchema(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	doc := taskWithArtifacts("in_review", "P1", askBody, []string{proj + "/" + deliveredLines})
	res := e.Run(proj, "s-evidence-absart", authPrompt, Turns("done", then(deliveryTurns(deliveredArtifact),
		srWrite("b1", taskPath, doc, citeUser(askQuote), citeTool(proofMarker)),
	)...))

	if !res.Refused() {
		t.Fatalf("an absolute-path artifact was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("the gate let an absolute-artifact task land on disk")
	}
	if !res.Saw("does not satisfy .sloprail/schemas/task.cue") {
		t.Errorf("the refusal was not the schema reason:\n%s", res.Output)
	}
}

// TestEvidence_ObservationsFieldRefusedBySchema: the retired `observations:` list
// of absolute transcript citations is no longer part of the closed schema, so a
// task still writing one is refused — the proof rides on the write now, not in a
// field that names a machine-local path.
func TestEvidence_ObservationsFieldRefusedBySchema(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	doc := "---\nstatus: backlog\npriority: P1\nobservations: [\"/abs/session.jsonl:12\"]\n---\n\n" + askBody + "\n"
	res := e.Run(proj, "s-evidence-observations", authPrompt, Turns("done",
		srWrite("b1", taskPath, doc, citeUser(askQuote)),
	))

	if !res.Refused() {
		t.Fatalf("a task carrying the retired observations field was not refused:\n%s", res.Output)
	}
	if !res.Saw("observations: field not allowed") {
		t.Errorf("the refusal was not the schema's closed-field reason:\n%s", res.Output)
	}
}

// TestEvidence_InvalidFrontmatterRefused: a task with status `done` — a status the
// schema does not have — is refused by the schema check. This is the "there is no
// done" invariant the whole task lifecycle rests on.
func TestEvidence_InvalidFrontmatterRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	// A cited body, so ONLY the schema can be what refuses this.
	res := e.Run(proj, "s-evidence-schema", authPrompt, Turns("done",
		srWrite("b1", taskPath, task("done", "P1", askBody), citeUser(askQuote)),
	))

	if !res.Refused() {
		t.Fatalf("a task with an invalid status was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("the gate let a status:done task land on disk")
	}
	if !res.Saw("does not satisfy .sloprail/schemas/task.cue") {
		t.Errorf("the refusal was not task-evidence's schema reason:\n%s", res.Output)
	}
}

// TestEvidence_InReviewNeedsArtifacts: an in_review task that cites real tool
// output but names NO artifact is refused — a claim of finished work must say
// where the result is, as well as prove it happened. The proof is real, so the
// refusal is specifically the missing artifacts.
func TestEvidence_InReviewNeedsArtifacts(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	doc := taskWithArtifacts("in_review", "P1", askBody, nil)
	res := e.Run(proj, "s-evidence-review-noart", authPrompt, Turns("done", then(deliveryTurns(deliveredArtifact),
		srWrite("b1", taskPath, doc, citeUser(askQuote), citeTool(proofMarker)),
	)...))

	if !res.Refused() {
		t.Fatalf("an in_review task missing its artifacts was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("the gate let an in_review task with no artifacts land")
	}
	if !res.Saw("EVIDENCE REQUIRED") || !res.Saw("names no artifacts") {
		t.Errorf("the refusal was not the missing-artifacts reason:\n%s", res.Output)
	}
	if res.Saw("a tool's output from this session") {
		t.Errorf("the refusal claims no tool output was cited, though one was:\n%s", res.Output)
	}
}

// TestEvidence_UnknownResultWriteRefused: a shell edit whose result the engine cannot
// compute ahead of the write (`sed -i`) reaches the task-evidence-resolves GATE with
// resultKnown false. A gate does not fail closed on its own, so the gate's script
// refuses it rather than admit bytes nobody saw; the file keeps its old status.
func TestEvidence_UnknownResultWriteRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, taskPath, task("backlog", "P1", askBody))
	installPluginTree(t, e, proj)
	// task-body's citation requirement would refuse the uncited shell edit first;
	// disabled so the refusal under test is the evidence gate's own.
	e.DisablePluginGuardrail(proj, pluginName+"/file-guard/task-body-is-human-authored", pluginName+"/gate/task-body-is-human-authored")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-evidence-sedi", authPrompt, Turns("done",
		Bash("b1", "sed -i.bak 's/status: backlog/status: to_do/' "+taskPath),
	))

	if !res.Refused() {
		t.Fatalf("a write whose result could not be computed was not refused:\n%s", res.Output)
	}
	if got := readFile(t, proj, taskPath); !strings.Contains(got, "status: backlog") {
		t.Errorf("the gate let an unverifiable edit land:\n%s", got)
	}
	if !res.Saw("could not be computed ahead of the write") {
		t.Errorf("the refusal did not say the result could not be computed:\n%s", res.Output)
	}
}
