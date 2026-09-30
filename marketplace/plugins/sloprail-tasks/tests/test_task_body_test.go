package e2e

import (
	"strings"
	"testing"
)

// task-body-is-human-authored is a file-guard over
// memories/tasks/<cat>/<name>/TASK.md with TWO checks in order:
//
//   0. REQUIRE: a write that CREATES the task or CHANGES its body (the guard's
//      `when: ./body-changed.sh`) must carry a citation of the user's own words —
//      `sr-file write|edit … --cite:user '<exact words>'`, resolved by the session
//      into `.event.citations`. A write leaving the body byte-identical (a status
//      edit) needs none. No citation where one is needed is refused by the engine,
//      before any check.
//   1. SCRIPT (body-is-stated.sh): the task has a body under its frontmatter.
//   2. PREPARE + JUDGE: the body must correspond to the cited words and hold THAT
//      AND NOTHING ELSE. The prepare skips the judge when the body did not change.
//      The judge is the model; its verdict is stubbed.
//
// The PreFileWrite gate refuses a not-fine write at PRE-tool, before it lands; the
// plain file-guard re-runs the same checks at Stop on the settled file (Post, against the session
// baseline).
//
// The judge verdict is a fixed stub (InstallJudgeClaude) — pass:true admits,
// pass:false refuses and the judge's reasoning reaches the agent. sr-file's
// citation resolution and stage 1 are NOT stubbed and run for real against the
// transcript the mock wrote.
//
// Tasks here rest at `backlog` / `blocked` so no-unfinished-work-at-turn-end (a
// to_do/in_progress task at Stop) never adds a block of its own.

// seedBaselineTask commits a TASK.md into the project before the session, so a
// write in the session is an UPDATE of it and the session baseline holds it.
func seedBaselineTask(t *testing.T, e *Env, proj, path, doc string) {
	t.Helper()
	e.WriteFile(proj, path, doc)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "seed a task")
}

// TestBody_CitedCreatePasses: a task created with sr-file, citing the user's own
// words, that the judge accepts, lands — and the judge was handed the cited words
// and where they resolved (the prepare builds its ground truth from
// .event.citations, not from anything in the file). The happy path and the control
// for every refusal below.
func TestBody_CitedCreatePasses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	sess := "s-body-ok"
	res := e.Run(proj, sess, authPrompt, Turns("done",
		srWrite("b1", taskPath, task("backlog", "P1", askBody), citeUser(askQuote)),
	))

	if res.Refused() {
		t.Fatalf("a cited, judge-accepted task body was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, taskPath) {
		t.Fatalf("an admitted body write did not land on disk:\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Errorf("a cited task body was blocked at Stop:\n%v", blocks)
	}
	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if !containsStr(prompt, "<citations>") || !containsStr(prompt, "<quote>"+askQuote) || !containsStr(prompt, "<message>") {
		t.Errorf("the judge was not handed the cited words and their location:\n%s", prompt)
	}
	// The engine recorded the citation for the path — the record
	// TestBody_CitedCreateSurvivesUncitedStatusEdit relies on seeing CLEARED.
	if rec := e.Meta(proj, sess, "citations"); !containsStr(rec, askQuote) {
		t.Errorf("the engine did not record the write's citation for the path:\n%s", rec)
	}
}

// TestBody_SlopBodyRefusedByJudge: a body whose write cites the user's words but
// which ALSO carries agent-authored elaboration the human never asked for — the
// "and nothing else" violation — passes the gate (a user citation is on the write)
// and lands; the file-guard's judge (stub pass:false) then blocks the turn at Stop,
// with the judge's reasoning reaching the agent.
func TestBody_SlopBodyRefusedByJudge(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "TASK BODY: the body adds acceptance criteria and a suggested approach the user never stated"}`)

	body := askBody + " Acceptance criteria: 100% test coverage, a rollback plan, and a metrics dashboard. Suggested approach: strangler-fig migration over three sprints."
	res := e.Run(proj, "s-body-slop", authPrompt, Turns("done",
		srWrite("b1", taskPath, task("backlog", "P1", body), citeUser(askQuote)),
	))

	if res.Refused() {
		t.Fatalf("the gate (citation only, no model) refused a cited body:\n%s", res.Output)
	}
	blocks := strings.Join(e.BlockingErrorsFrom(proj, "s-body-slop", "Stop"), "\n")
	if !strings.Contains(blocks, "acceptance criteria and a suggested approach the user never stated") {
		t.Errorf("a slop body the judge rejected was not blocked at Stop with its reasoning:\n%s", blocks)
	}
}

// TestBody_UncitedCreateRefusedByRequire: creating a task with the Write tool —
// which cannot carry a citation — is refused by the guard's declared citation
// requirement, before any check, and the refusal names the exact grounded form. The stub is
// PASS: if the engine reached the judge or admitted, the write would go through.
func TestBody_UncitedCreateRefusedByRequire(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-body-nocite", authPrompt, Turns("done",
		Write("w1", taskPath, task("backlog", "P1", askBody)),
	))

	if !res.Refused() {
		t.Fatalf("an uncited task create was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("the gate let an uncited task land on disk")
	}
	if !res.Saw("must cite the user's own words (--cite:user)") {
		t.Errorf("the refusal was not the citation requirement's reason:\n%s", res.Output)
	}
	if !res.Saw("sr-file write "+taskPath) || !res.Saw("--cite:user") {
		t.Errorf("the refusal does not tell the agent the exact sr-file form to use:\n%s", res.Output)
	}
}

// TestBody_UncitedBodyChangeRefused: a task already in the tree has its BODY
// changed by a write carrying no citation (the Write tool, same frontmatter). The
// body is the oracle; rewriting it ungrounded is the exact move this guard exists
// to refuse. It never lands.
func TestBody_UncitedBodyChangeRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	original := task("backlog", "P1", askBody)
	seedBaselineTask(t, e, proj, taskPath, original)

	softened := task("backlog", "P1", "Migrate the parts of the auth module that were easy.")
	res := e.Run(proj, "s-body-change-nocite", authPrompt, Turns("done",
		Write("w1", taskPath, softened),
	))

	if !res.Refused() {
		t.Fatalf("an uncited change to a task's body was not refused:\n%s", res.Output)
	}
	if !res.Saw("this change to " + taskPath + " must cite the user's own words") {
		t.Errorf("the refusal was not the citation requirement's reason:\n%s", res.Output)
	}
	if got := readFile(t, proj, taskPath); got != original {
		t.Errorf("the refused body change reached the file:\n%s", got)
	}
}

// TestBody_StatusOnlyChangePermittedWithoutCitation: a task already in the tree
// has ONLY its status changed (backlog -> blocked) with the plain Write tool. The
// body is byte-identical, so nothing new needs grounding: permitted, lands, and —
// because the prepare skips — the judge is never called. The capturing stub is
// FAIL, so a judge run would both refuse and leave a prompt behind.
func TestBody_StatusOnlyChangePermittedWithoutCitation(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": false, "reasoning": "TASK BODY: the judge must not have run"}`)

	seedBaselineTask(t, e, proj, taskPath, task("backlog", "P1", askBody))

	sess := "s-body-status-only"
	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, task("blocked", "P1", askBody)),
	))

	if res.Refused() {
		t.Fatalf("a status-only change was refused though the body is unchanged:\n%s", res.Output)
	}
	if got := readFile(t, proj, taskPath); !strings.Contains(got, "status: blocked") {
		t.Errorf("the status-only change did not land:\n%s", got)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Errorf("a status-only change was blocked at Stop:\n%v", blocks)
	}
	if prompt := e.JudgePrompt(proj, "judge-prompt.txt"); prompt != "" {
		t.Errorf("the body judge ran on a status-only change (%d-byte prompt); its prepare should have skipped it", len(prompt))
	}
}

// TestBody_CitedCreateSurvivesUncitedStatusEdit: the ordinary flow — a task is
// created with a citation, then its status is changed with the plain Write tool.
// The engine keeps the path's recorded citations through that uncited write
// (cited changes accumulate; an uncited one leaves them in place), so the Post
// event at Stop still carries the ask's citation and a grounded task is not
// refused at the end of the turn. The Stop judge is handed that citation.
func TestBody_CitedCreateSurvivesUncitedStatusEdit(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	sess := "s-body-create-then-status"
	res := e.Run(proj, sess, authPrompt, Turns("done",
		srWrite("b1", taskPath, task("backlog", "P1", askBody), citeUser(askQuote)),
		Write("w2", taskPath, task("blocked", "P1", askBody)),
	))

	if res.Refused() {
		t.Fatalf("the cited create or the status edit was refused at Pre:\n%s", res.Output)
	}
	if got := readFile(t, proj, taskPath); !strings.Contains(got, "status: blocked") {
		t.Fatalf("the status edit did not land:\n%s", got)
	}
	// The engine's own record of the path's citations survived the uncited edit —
	// the Post event at Stop is built from it.
	if rec := e.Meta(proj, sess, "citations"); !containsStr(rec, askQuote) {
		t.Fatalf("the engine dropped the path's citation on an uncited edit:\n%s", rec)
	}
	for _, b := range e.BlockingErrorsFrom(proj, sess, "Stop") {
		if containsStr(b, "TASK BODY") {
			t.Fatalf("a body grounded at creation was refused at Stop after an uncited status edit:\n%s", b)
		}
	}
	if prompt := e.JudgePrompt(proj, "judge-prompt.txt"); !containsStr(prompt, askQuote) {
		t.Errorf("the Stop judge was not handed the citation recorded for this body:\n%s", prompt)
	}
	// The judge lives in the file-guard: the body is judged once, as it settled
	// (Stop) — never at the create, never for the status edit.
	if n := e.JudgeCalls(proj, "judge-prompt.txt", "BODY of a task file"); n != 1 {
		t.Errorf("the body judge was asked %d times, want 1 (Stop)", n)
	}
}
