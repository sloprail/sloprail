package e2e

import "testing"

// task-dependencies-resolve is a file-guard over
// memories/tasks/<cat>/<name>/TASK.md with one deterministic SCRIPT check: every
// id in depends_on must resolve to a task folder that no longer exists (done
// means deleted, per the plugin's lifecycle), and the depends_on graph must have
// no cycle reachable from the task's own id. A not-fine write is refused at PRE-tool by the
// gate, before it lands.
//
// This guard fires on entering to_do/in_progress/in_review -- moving among
// backlog/blocked, or staying in to_do/in_progress, costs nothing.
//
// depends_on deliberately does NOT try to distinguish "genuinely unfinished"
// from "never a real task" (a typo): both read identically as "this id resolves
// to nothing right now." What makes that safe is the no-dangling-dependencies
// invariant (its own test file) -- an id can never legitimately mean "used to
// exist, now done" while looking the same as "never existed."
//
// These prove: moving to in_progress with an unfinished dependency is refused,
// and permitted once the dependency folder is gone; a dependency cycle is
// refused; an id that was never a real task is permitted by THIS guard (the
// deliberate gap the Stop gate closes).

// depTaskPath is a second task's path under a different group, so the two never
// collide in the same tests/ regex match.
const depTaskPath = "memories/tasks/infra/setup-ci/TASK.md"

// TestDeps_UnfinishedDependencyBlocksThenPermits: a task depending on
// infra/setup-ci is refused while that task's folder exists, and permitted once
// it is gone -- the control that isolates task-dependencies-resolve's own
// refusal from everything else in the plugin (task-body's judge is stubbed
// PASS and the write cites the user's words, so the body is no obstacle).
func TestDeps_UnfinishedDependencyBlocksThenPermits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	// The dependency: a real task folder, committed as part of the baseline so
	// it is not read as this session's own diff.
	e.WriteFile(proj, depTaskPath, task("to_do", "P1", "Set up CI."))
	e.CommitAll(proj, "seed the dependency task")

	sess := "s-deps-unfinished"
	depsDoc := "---\nstatus: to_do\npriority: P1\ndepends_on: [\"infra/setup-ci\"]\n---\n\n" + askBody + "\n"

	res := e.Run(proj, sess, authPrompt, Turns("done",
		srWrite("b1", taskPath, depsDoc, citeUser(askQuote)),
	))
	if !res.Refused() {
		t.Fatalf("a task depending on an unfinished task was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("the gate let a task with an unfinished dependency land")
	}
	if !res.Saw("still exists") {
		t.Errorf("the refusal was not the unfinished-dependency reason:\n%s", res.Output)
	}

	// Remove the dependency folder (simulating reviewer approval) and retry.
	e.Git(proj, "rm", "-r", "memories/tasks/infra/setup-ci")
	e.Git(proj, "commit", "-m", "dependency approved and deleted")

	// A second human turn asks again. Its own words ground the retry: the first
	// prompt's words now appear twice in the record (a resumed run appends its
	// prompt), and a quote matching two messages resolves to neither.
	res2 := e.Run(proj, sess, "CI is set up now, so file the token migration task again.", Turns("done",
		srWrite("b2", taskPath, depsDoc, citeUser("file the token migration task again")),
	).ThenCommit("File the token migration task", CitesUser("file the token migration task again")))
	if res2.Refused() {
		t.Fatalf("a task whose dependency is gone was still refused:\n%s", res2.Output)
	}
	if !e.Exists(proj, taskPath) {
		t.Errorf("an admitted task write did not land on disk")
	}
}

// TestDeps_CycleRefused: task A depends on task B, and the write under test
// makes task B depend on task A -- closing a cycle. Refused before it lands.
func TestDeps_CycleRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-deps-cycle"

	// Task B (infra/setup-ci) already depends on task A (auth/migrate-tokens) --
	// committed as baseline.
	bDoc := "---\nstatus: backlog\npriority: P1\ndepends_on: [\"auth/migrate-tokens\"]\n---\n\nSet up CI, blocked on the auth migration.\n"
	e.WriteFile(proj, depTaskPath, bDoc)
	e.CommitAll(proj, "seed task B depending on task A")

	// Now task A (this write) tries to depend on task B -- a cycle: A -> B -> A.
	aDoc := "---\nstatus: to_do\npriority: P1\ndepends_on: [\"infra/setup-ci\"]\n---\n\n" + askBody + "\n"

	res := e.Run(proj, sess, authPrompt, Turns("done",
		srWrite("b1", taskPath, aDoc, citeUser(askQuote)),
	))
	if !res.Refused() {
		t.Fatalf("a depends_on cycle was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, taskPath) {
		t.Errorf("the gate let a cyclic dependency land")
	}
	if !res.Saw("CYCLE") {
		t.Errorf("the refusal was not the cycle reason:\n%s", res.Output)
	}
}

// TestDeps_UnknownIdIsRefused pins the deliberate non-distinction the header
// comment argues for: a depends_on id naming a task that was never real reads
// IDENTICALLY to an unfinished one from a live folder scan alone, so
// task-dependencies-resolve permits it just as it would a real, unfinished
// dependency -- it does NOT try to detect "never existed." Despite the test's
// name, the assertion is that THIS guard does not refuse it; the real
// protection against a dangling/fabricated id sits in
// no-dangling-dependencies-at-turn-end.
func TestDeps_UnknownIdIsRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-deps-unknown"

	// "nobody/never-existed" never had a task folder, in this session or in
	// any baseline commit.
	doc := "---\nstatus: to_do\npriority: P1\ndepends_on: [\"nobody/never-existed\"]\n---\n\n" + askBody + "\n"

	res := e.Run(proj, sess, authPrompt, Turns("done",
		srWrite("b1", taskPath, doc, citeUser(askQuote)),
	).ThenCommit("File the task", CitesUser(askQuote)))
	if res.Refused() {
		t.Fatalf("an id naming a task that never existed was refused by task-dependencies-resolve -- this guard treats a non-existent folder as \"done\" regardless of whether it was ever real; if this now refuses, the design note in check-dependencies.sh is stale and this test's expectation should be revisited together with it:\n%s", res.Output)
	}
	if !e.Exists(proj, taskPath) {
		t.Errorf("a write task-dependencies-resolve permits should land on disk")
	}
}
