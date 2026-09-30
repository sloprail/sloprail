package e2e

import (
	"path/filepath"
	"testing"
)

// task-md-first is a PREVENTIVE file-guard, script only, path-based: any file
// inside a task folder (memories/tasks/<group>/<task>/, a gates/ file
// included) other than TASK.md may only be written once that task's own
// TASK.md exists on disk. Without it, a folder's TASK.md-less files are
// orphaned — every other guard in this plugin keys off TASK.md's
// frontmatter or body. The same failure unit-md-first guards for a content
// unit in sloprail-content, relocated to the task shape.
//
// installTaskMdFirstProject stands up a project with every OTHER guard's
// judge stubbed to PASS, since a gates/ write also crosses
// task-gate-is-grounded's judge and this file is not about that guard.
func installTaskMdFirstProject(t *testing.T) (*Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
	return e, proj
}

// taskDir is the task folder every scenario below writes into:
// memories/tasks/auth/migrate-tokens (taskPath's parent, already defined in
// main_test.go).
var taskDir = filepath.Dir(taskPath)

// TestTaskMdFirst_NonEntryFileBeforeTaskMdRefused: writing a supporting file
// directly under a task folder that has no TASK.md yet is refused before it
// lands, and the refusal names the missing TASK.md and how to write it.
func TestTaskMdFirst_NonEntryFileBeforeTaskMdRefused(t *testing.T) {
	e, proj := installTaskMdFirstProject(t)

	notes := taskDir + "/notes.md"
	res := e.Run(proj, "s-taskmd-before", authPrompt, Turns("done",
		Write("w1", notes, "# migration notes\n"),
	))
	if !res.Refused() {
		t.Fatalf("a supporting file written before TASK.md existed was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, notes) {
		t.Errorf("the preventive guard let a supporting file land before TASK.md existed")
	}
	if !res.Saw(taskDir+" has no TASK.md") || !res.Saw("Write "+taskDir+"/TASK.md first") {
		t.Errorf("the refusal does not name the missing TASK.md and how to fix it:\n%s", res.Output)
	}
}

// TestTaskMdFirst_GateFileBeforeTaskMdRefused: the same refusal for a file
// ONE level deeper, under gates/ — the task folder is computed as the first
// two segments after memories/tasks/, not simply the file's own parent
// directory, which for a gates file would be .../gates, never where TASK.md
// lives.
func TestTaskMdFirst_GateFileBeforeTaskMdRefused(t *testing.T) {
	e, proj := installTaskMdFirstProject(t)

	gate := taskDir + "/gates/repo-is-public.sh"
	res := e.Run(proj, "s-taskmd-gate-before", authPrompt, Turns("done",
		Write("w1", gate, "#!/bin/sh\nexit 0\n"),
	))
	if !res.Refused() {
		t.Fatalf("a gate file written before TASK.md existed was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, gate) {
		t.Errorf("the preventive guard let a gate file land before TASK.md existed")
	}
	if !res.Saw(taskDir + " has no TASK.md") {
		t.Errorf("the refusal did not name the missing TASK.md (task folder resolved wrong for a gates/ file?):\n%s", res.Output)
	}
}

// TestTaskMdFirst_TaskMdItselfAdmitted: writing TASK.md itself into a task
// folder that has none yet is admitted — the guard's match excludes TASK.md,
// so nothing here blocks the file that satisfies the rule for every other
// file in the folder.
func TestTaskMdFirst_TaskMdItselfAdmitted(t *testing.T) {
	e, proj := installTaskMdFirstProject(t)

	res := e.Run(proj, "s-taskmd-itself", authPrompt, Turns("done",
		srWrite("b1", taskPath, task("backlog", "P1", askBody), citeUser(askQuote)),
	))
	if res.Refused() {
		t.Fatalf("writing TASK.md itself was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, taskPath) {
		t.Errorf("an admitted TASK.md write did not land on disk")
	}
}

// TestTaskMdFirst_NonEntryFileAfterTaskMdAdmitted: TASK.md is written first,
// then a supporting file — in the SAME run, so the pre-write check for the
// second write sees TASK.md already on disk. Both land.
func TestTaskMdFirst_NonEntryFileAfterTaskMdAdmitted(t *testing.T) {
	e, proj := installTaskMdFirstProject(t)

	notes := taskDir + "/notes.md"
	res := e.Run(proj, "s-taskmd-after", authPrompt, Turns("done",
		srWrite("b1", taskPath, task("backlog", "P1", askBody), citeUser(askQuote)),
		Write("w2", notes, "# migration notes\n"),
	))
	if res.Refused() {
		t.Fatalf("a supporting file written after TASK.md existed was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, taskPath) || !e.Exists(proj, notes) {
		t.Errorf("an admitted TASK.md + supporting-file pair did not both land on disk")
	}
}

// TestTaskMdFirst_DeletionNotRefused: a task folder already on disk (TASK.md
// and a supporting file, seeded into the session baseline) is deleted whole
// — the reviewer's own approve-path move once task-review passes. deletions
// is left at its default (skip), so this guard is not even asked about
// either delete, TASK.md's included, and neither is refused.
func TestTaskMdFirst_DeletionNotRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	notes := taskDir + "/notes.md"
	e.WriteFile(proj, taskPath, task("backlog", "P1", askBody))
	e.WriteFile(proj, notes, "# migration notes\n")
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-taskmd-delete", authPrompt, Turns("done",
		Bash("b1", "rm -r "+taskDir),
	))
	if res.Refused() {
		t.Fatalf("deleting a whole task folder was refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) || e.Exists(proj, notes) {
		t.Errorf("the permitted delete did not remove the task folder")
	}
}
