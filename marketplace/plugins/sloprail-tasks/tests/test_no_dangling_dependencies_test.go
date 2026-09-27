package e2e

import "testing"

// no-dangling-dependencies-at-turn-end is a GATE on Stop (no match -- Stop
// carries no fields). It walks the whole task tree and refuses the turn if any
// task's depends_on names a folder that does not exist. This is what makes
// deleting a task and stripping it from every dependent's depends_on ONE
// change: task-dependencies-resolve only refuses a DEPENDENT from entering
// to_do/in_progress/in_review while its own dependency is missing; nothing
// else re-checked depends_on on a task that had ALREADY passed that gate --
// until this one.
//
// These prove: deleting a dependency WITHOUT stripping it from a dependent's
// depends_on is refused at Stop; deleting it WITH the strip (in the same turn)
// permits the turn to end.

const danglingDepPath = "memories/tasks/infra/setup-ci/TASK.md"

// TestDangling_DeleteWithoutStrippingRefusesAtStop: a dependent task
// (auth/migrate-tokens) depends on infra/setup-ci, which exists at baseline
// (so the dependent could legitimately be written while unfinished). The
// agent then deletes infra/setup-ci WITHOUT touching the dependent's
// depends_on -- a dangling reference -- and the turn is blocked at Stop.
func TestDangling_DeleteWithoutStrippingRefusesAtStop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	// Baseline: the dependency task AND the dependent, already correctly
	// pointing at it (depends_on is only re-verified going forward by the
	// preventive guard -- writing a baseline both tasks lets this test isolate
	// the Stop gate's OWN behaviour rather than task-dependencies-resolve's).
	e.WriteFile(proj, danglingDepPath, task("to_do", "P1", "Set up CI."))
	dependentDoc := "---\nstatus: to_do\npriority: P1\ndepends_on: [\"infra/setup-ci\"]\n---\n\nDepends on CI. Placeholder body, no citation needed for a BASELINE file the guard never Pre-checked.\n"
	e.WriteFile(proj, taskPath, dependentDoc)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "seed dependent and dependency, both correct")

	sess := "s-dangling-nostrip"

	// The agent deletes the dependency's folder, and does NOTHING else --
	// leaving the dependent's depends_on pointing at nothing.
	res := e.Run(proj, sess, authPrompt, Turns("done",
		Bash("b1", "rm -rf memories/tasks/infra/setup-ci"),
	))
	if res.Refused() {
		t.Fatalf("the delete itself was refused at Pre (setup broken):\n%s", res.Output)
	}
	if e.Exists(proj, danglingDepPath) {
		t.Fatalf("the rm did not remove the dependency folder:\n%s", res.Output)
	}

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a turn that deleted a dependency without stripping it from a dependent was not blocked at Stop:\n%s", res.Output)
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	if !containsStr(joined, "dangling dependenc") {
		t.Errorf("the Stop block was not the dangling-dependency refusal:\n%s", joined)
	}
	if !containsStr(joined, "auth/migrate-tokens") || !containsStr(joined, "infra/setup-ci") {
		t.Errorf("the refusal did not name the dependent and the missing dependency:\n%s", joined)
	}
}

// TestDangling_DeleteWithStripPermits: the SAME setup, but the agent deletes
// the dependency AND rewrites the dependent to drop the id from its
// depends_on, in the SAME turn. The turn ends cleanly.
func TestDangling_DeleteWithStripPermits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	e.WriteFile(proj, danglingDepPath, task("to_do", "P1", "Set up CI."))
	dependentDoc := "---\nstatus: to_do\npriority: P1\ndepends_on: [\"infra/setup-ci\"]\n---\n\nDepends on CI. Placeholder body, no citation needed for a BASELINE file the guard never Pre-checked.\n"
	e.WriteFile(proj, taskPath, dependentDoc)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "seed dependent and dependency, both correct")

	sess := "s-dangling-strip"
	// The rewritten dependent: depends_on stripped, body untouched. Stripping an
	// id is a frontmatter-only change, so task-body-is-human-authored needs no
	// citation for it — the plain Write tool is enough.
	strippedDoc := "---\nstatus: to_do\npriority: P1\n---\n\nDepends on CI. Placeholder body, no citation needed for a BASELINE file the guard never Pre-checked.\n"

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Bash("b1", "rm -rf memories/tasks/infra/setup-ci"),
		Write("w1", taskPath, strippedDoc),
	))
	if res.Refused() {
		t.Fatalf("the delete-and-strip turn was refused at Pre:\n%s", res.Output)
	}
	if e.Exists(proj, danglingDepPath) {
		t.Fatalf("the dependency folder still exists:\n%s", res.Output)
	}

	for _, b := range e.BlockingErrorsFrom(proj, sess, "Stop") {
		if containsStr(b, "dangling dependenc") {
			t.Fatalf("the gate blocked a turn that stripped the dependency in the same change:\n%s", b)
		}
	}
}
