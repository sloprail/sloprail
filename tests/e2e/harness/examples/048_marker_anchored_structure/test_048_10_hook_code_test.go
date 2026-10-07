package e2e

import "testing"

// T048_10: the doc exclusion is markdown under .claude/{skills,commands,agents}
// only. A hook under .claude/hooks/ is code and still falls under the rule.
func TestT048_10_ClaudeHookCodeIsStillGuarded(t *testing.T) {
	e := newEnv(t)
	proj := masProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "judge would pass; the file name must refuse"}`)

	e.Run(proj, "s-048-10", "add a hook", Turns("done",
		Write("w1", ".claude/hooks/x.py", "# sr:endpoint \"<verb>-<resource>\"\ndef run():\n    pass\n"),
	).ThenCommit("add the hook"))
	if blocks := e.BlockingErrorsFrom(proj, "s-048-10", "Stop"); len(blocks) == 0 {
		t.Fatalf("a hook under .claude/hooks/ carrying an endpoint marker was not refused")
	}
}
