package e2e

import "testing"

// One range, several tasks. A file-guard over task files is evaluated once over
// the whole changeset (every TASK.md changed since the rule last passed), so a
// single bad task among good ones must refuse the Stop and be NAMED; the good
// one is not blamed, and fixing the bad one lets the range pass.

const (
	multiGoodPath = "memories/tasks/auth/migrate-tokens/TASK.md" // == taskPath
	multiBadPath  = "memories/tasks/web/ship-it/TASK.md"
)

// TestMultiTask_OneBadTaskInARangeRefusesNamingIt: two TASK.md files are
// committed in one commit. One is an ordinary backlog task; the other enters
// to_do while the task it depends_on (infra/setup-ci) still exists —
// task-dependencies-resolve's refusal. The Stop is refused, the refusal names the
// bad task's path and not the good one's; rewriting the bad task without the
// dependency passes the same session.
func TestMultiTask_OneBadTaskInARangeRefusesNamingIt(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	// An open task at turn end is a different guardrail's business (it would refuse
	// this range for the to_do task whatever its dependencies).
	e.DisablePluginGuardrail(proj, pluginName+"/gate/no-unfinished-work-at-turn-end")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	e.WriteFile(proj, depTaskPath, task("backlog", "P1", "Set up CI."))
	e.CommitAll(proj, "seed the dependency task")

	e.WriteFile(proj, multiGoodPath, task("backlog", "P1", askBody))
	e.WriteFile(proj, multiBadPath, "---\nstatus: to_do\npriority: P1\ndepends_on: [\"infra/setup-ci\"]\n---\n\n"+askBody+"\n")

	sess := "s-multi-task"
	e.Run(proj, sess, authPrompt, Turns("done").ThenCommit("File both tasks", CitesUser(askQuote)))

	joined := stopBlocks(e, proj, sess)
	if !containsStr(joined, "still exists") {
		t.Fatalf("the task with an unfinished dependency did not refuse the range:\n%s", joined)
	}
	if !containsStr(joined, multiBadPath) {
		t.Errorf("the refusal does not name the bad task %s:\n%s", multiBadPath, joined)
	}
	if containsStr(joined, multiGoodPath) {
		t.Errorf("the refusal blames the good task %s:\n%s", multiGoodPath, joined)
	}
	seen := len(e.StopContinuations(proj, sess))

	// Rewritten without the dependency, the same range passes.
	e.WriteFile(proj, multiBadPath, task("to_do", "P1", askBody))
	e.Run(proj, sess, "drop the dependency", Turns("done").ThenCommit("Drop the dependency", CitesUser(askQuote)))
	if n := len(e.StopContinuations(proj, sess)); n != seen {
		t.Fatalf("the range with the bad task fixed was still refused:\n%s", stopBlocks(e, proj, sess))
	}
}
