package e2e

import "testing"

// task-evidence-resolves is a PREVENTIVE file-guard over
// memories/tasks/<cat>/<name>/TASK.md with one deterministic SCRIPT check: the
// frontmatter must satisfy task.cue, and every DELIVERY-evidence citation must
// resolve to the thing its KIND promises. Being preventive, a not-fine write is
// refused at PRE-tool, before it lands.
//
// The two evidence kinds are FRONTMATTER citation strings with DIFFERENT PATH BASES:
//
//   observations: ["/abs/session.jsonl:120"]   ABSOLUTE .jsonl — every cited line
//                                               must be a tool_result the session
//                                               produced (via trajectory tool-result)
//   artifacts:    ["src/auth.go:10-40"]         REPO-RELATIVE tree file — the path
//                                               exists under the repo, lines exist
//
// This is the DETERMINISTIC half of the review lifecycle: it answers only "do the
// citations resolve", the gate task-review's judge depends on. It does NOT judge
// substantiation (that is task-review's model call), and it does NOT check the
// BODY's citation of the ASK (that is task-body-is-human-authored, grounded against
// the user pool). The three are distinct — see the plugin README.
//
// task-evidence-resolves's OWN check is deterministic — no judge. But
// task-body-is-human-authored guards the SAME path and DOES have a judge (its stage
// 2), which fires on the body's citation. Where a test's body carries a grounded ask
// citation, a passing judge stub (pass:true) is installed so task-body permits and
// what task-evidence does is isolated. The tests that need no stub are the ones
// whose refusal happens before any judge (a bad frontmatter, or a body that fails
// task-body's own deterministic stage 1).
//
// These prove: a task with resolving delivery evidence PERMITS and lands; an
// observation citing a line that is NOT a tool_result is REFUSED; an artifact that is
// absolute, a .jsonl, or absent from the tree is REFUSED; an invalid-frontmatter task
// (status: done) is refused by the schema; and an in_review task missing a kind of
// evidence is refused.

// prepareEvidence drives run 1 for the task-evidence tests: it produces a tool_result
// carrying marker and writes the artifact file, returning the tool_result's line so
// run 2 can cite it. (Mirror of the review suite's prepareDelivery — the delivery
// exists before the task claims it.)
func prepareEvidence(t *testing.T, e *Env, proj, sess, marker, artifactPath string) int {
	t.Helper()
	// Write FIRST so the artifact file lands (a real tool_use the mock executes),
	// THEN the ToolResult carrying the marker — see prepareDelivery for why the order
	// matters (a leading ToolResult ends the turn before the Write fires).
	e.Run(proj, sess, authPrompt, Turns("done",
		Write("wf", artifactPath, "package auth\n\n// migrated\nfunc Migrate() error {\n\treturn nil\n}\n"),
		ToolResult("r1", "go test ./auth/...\nok  sloprail/auth  0.4s\n"+marker),
	))
	tp := e.TranscriptPath(proj, sess)
	line := toolResultLine(t, tp, marker)
	if line == 0 {
		t.Fatalf("run 1 did not put the tool_result on the transcript:\n%s", transcriptText(t, tp))
	}
	return line
}

// TestEvidence_ResolvingEvidencePermits: an in_review task whose observation cites a
// real tool_result line and whose artifact cites a real repo-relative file:line
// permits, and the file lands. The body cites the ask (grounded → task-body's stage 1
// passes, judge stubbed PASS), so the only thing that could refuse the delivery
// evidence is task-evidence, which must not. This is the control for every refusal
// below.
func TestEvidence_ResolvingEvidencePermits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-evidence-ok"
	artifactRel := "src/auth.go"
	line := prepareEvidence(t, e, proj, sess, "PASS", artifactRel)
	tp := e.TranscriptPath(proj, sess)

	body := "Done. The user asked to " + cite("migrate the auth module", tp, 1) + "."
	obs := []string{tp + ":" + itoa(line)}
	art := []string{artifactRel + ":3-5"}

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, taskWithEvidence("in_review", "P1", body, obs, art)),
	))

	if res.Refused() {
		t.Fatalf("a task with resolving delivery evidence was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, taskPath) {
		t.Errorf("an admitted task write did not land on disk")
	}
}

// TestEvidence_ObservationNotAToolResultRefused: an observation citing a transcript
// line that is NOT a tool_result (here line 1, the user's prompt) is refused at
// pre-tool — the deterministic check confirms each observation line is a tool_result
// and this one is not. The write never lands (preventive). This is the exact gap the
// old rule named as missing: an agent citing its own prose (or the user's ask) as
// proof of work.
func TestEvidence_ObservationNotAToolResultRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-evidence-notresult"
	artifactRel := "src/auth.go"
	_ = prepareEvidence(t, e, proj, sess, "PASS", artifactRel)
	tp := e.TranscriptPath(proj, sess)

	body := "Done. The user asked to " + cite("migrate the auth module", tp, 1) + "."
	obs := []string{tp + ":1"} // line 1 is the user's prompt, not a tool_result
	art := []string{artifactRel + ":3-5"}

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, taskWithEvidence("in_review", "P1", body, obs, art)),
	))

	if !res.Refused() {
		t.Fatalf("an observation citing a non-tool_result line was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("the preventive guard let a non-tool_result observation land on disk")
	}
	if !res.Saw("is NOT a tool_result") {
		t.Errorf("the refusal was not the not-a-tool_result reason:\n%s", res.Output)
	}
}

// TestEvidence_ObservationCitingAnswerEnvelopeRefused is the adversarial case: an
// AskUserQuestion answer re-enters the transcript as a tool_result BLOCK (its body
// is `The user answered: ...`), so a naive "is there a tool_result at this line"
// check would classify the USER's own answer as delivery proof — the exact
// substitution this whole rework exists to refuse, relocated from the body-citation
// path into the observation path. An agent could move a task to in_review citing the
// line of its own "yes, proceed" as an OBSERVATION. It must be refused: the answer
// envelope is the user's words (the SourceUser pool), never a produced result.
func TestEvidence_ObservationCitingAnswerEnvelopeRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-evidence-answerenv"
	artifactRel := "src/auth.go"
	// Run 1: write the artifact AND emit an AskUserQuestion answer envelope (the
	// adversarial "result"). Write first so the file lands before the turn ends.
	e.Run(proj, sess, authPrompt, Turns("done",
		Write("wf", artifactRel, "package auth\n\nfunc Migrate() error {\n\treturn nil\n}\n"),
		AnswerQuestion("q1", [2]string{"proceed with the migration?", "yes, proceed"}),
	))
	tp := e.TranscriptPath(proj, sess)

	// The answer envelope's physical line — distinctively the one carrying the
	// `The user answered:` body. This is the line an attacker would cite.
	envLine := answerEnvelopeLine(t, tp)
	if envLine == 0 {
		t.Fatalf("run 1 did not put an answer envelope on the transcript:\n%s", transcriptText(t, tp))
	}

	body := "Done. The user asked to " + cite("migrate the auth module", tp, 1) + "."
	obs := []string{tp + ":" + itoa(envLine)} // the user's ANSWER, masquerading as delivery
	art := []string{artifactRel + ":3-4"}

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, taskWithEvidence("in_review", "P1", body, obs, art)),
	))

	if !res.Refused() {
		t.Fatalf("an observation citing an AskUserQuestion answer envelope was not refused — the user's own answer passed as delivery proof:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("the preventive guard let an answer-envelope observation land on disk")
	}
	if !res.Saw("is NOT a tool_result") {
		t.Errorf("the refusal was not the not-a-tool_result reason:\n%s", res.Output)
	}
}

// TestEvidence_ArtifactMissingFromTreeRefused: an artifact citing a repo-relative
// file that is not in the tree is refused — the file-citation check resolves it under
// the repo root and finds nothing. The write never lands.
func TestEvidence_ArtifactMissingFromTreeRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-evidence-noart"
	artifactRel := "src/auth.go"
	line := prepareEvidence(t, e, proj, sess, "PASS", artifactRel)
	tp := e.TranscriptPath(proj, sess)

	body := "Done. The user asked to " + cite("migrate the auth module", tp, 1) + "."
	obs := []string{tp + ":" + itoa(line)}
	art := []string{"src/does-not-exist.go:1-3"} // never written to the tree

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, taskWithEvidence("in_review", "P1", body, obs, art)),
	))

	if !res.Refused() {
		t.Fatalf("an artifact absent from the tree was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("the preventive guard let a missing-artifact task land on disk")
	}
	if !res.Saw("not in the tree") {
		t.Errorf("the refusal was not the missing-artifact reason:\n%s", res.Output)
	}
}

// TestEvidence_AbsoluteArtifactRefusedBySchema: an artifact with an ABSOLUTE path is
// a mis-filed citation — an absolute path is an observation's transcript, not a
// repo-relative produced file. task.cue's _artifact regex refuses it at the schema
// check, before resolution. This pins the path-base convention: artifacts are
// repo-relative, observations are absolute.
func TestEvidence_AbsoluteArtifactRefusedBySchema(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-evidence-absart"
	artifactRel := "src/auth.go"
	line := prepareEvidence(t, e, proj, sess, "PASS", artifactRel)
	tp := e.TranscriptPath(proj, sess)

	body := "Done. The user asked to " + cite("migrate the auth module", tp, 1) + "."
	obs := []string{tp + ":" + itoa(line)}
	// An ABSOLUTE artifact path — the schema's _artifact regex forbids a leading /.
	art := []string{proj + "/src/auth.go:3-5"}

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, taskWithEvidence("in_review", "P1", body, obs, art)),
	))

	if !res.Refused() {
		t.Fatalf("an absolute-path artifact was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("the preventive guard let an absolute-artifact task land on disk")
	}
	if !res.Saw("does not satisfy .sloprail/schemas/task.cue") {
		t.Errorf("the refusal was not the schema reason:\n%s", res.Output)
	}
}

// TestEvidence_InvalidFrontmatterRefused: a task with status `done` — a status the
// schema does not have — is refused by the schema check, before any citation is
// looked at. This is the "there is no done" invariant the whole task lifecycle rests
// on.
func TestEvidence_InvalidFrontmatterRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-evidence-schema"
	tp := e.TranscriptPath(proj, sess)
	// A grounded body citation, so ONLY the schema can be what refuses this.
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
	if !res.Saw("does not satisfy .sloprail/schemas/task.cue") {
		t.Errorf("the refusal was not task-evidence's schema reason:\n%s", res.Output)
	}
}

// TestEvidence_InReviewNeedsBothKinds: an in_review task with an observation but NO
// artifact is refused for missing evidence — a claim of finished work must carry both
// proof it happened AND where the result is. This is the deterministic half of the
// in_review requirement (task-review's judge is the other half). The observation is
// real (so the refusal is specifically the missing artifact, not a broken
// observation).
func TestEvidence_InReviewNeedsBothKinds(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-evidence-review-noart"
	artifactRel := "src/auth.go"
	line := prepareEvidence(t, e, proj, sess, "PASS", artifactRel)
	tp := e.TranscriptPath(proj, sess)

	body := "Done. The user asked to " + cite("migrate the auth module", tp, 1) + "."
	obs := []string{tp + ":" + itoa(line)}

	res := e.Run(proj, sess, authPrompt, Turns("done",
		// Only observations, no artifacts.
		Write("w1", taskPath, taskWithEvidence("in_review", "P1", body, obs, nil)),
	))

	if !res.Refused() {
		t.Fatalf("an in_review task missing its artifacts was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("the preventive guard let an in_review task with no artifacts land")
	}
	if !res.Saw("missing its delivery evidence") {
		t.Errorf("the refusal was not the missing-evidence reason:\n%s", res.Output)
	}
}
