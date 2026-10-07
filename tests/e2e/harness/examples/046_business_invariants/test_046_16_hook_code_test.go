package e2e

import "testing"

// T046_16: the doc exclusion is markdown under .claude/{skills,commands,agents}
// only. A hook under .claude/hooks/ is code, and a placeholder pin in it is still
// refused.
func TestT046_16_ClaudeHookCodeIsStillGuarded(t *testing.T) {
	e := newEnv(t)
	proj := biProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the script refuses first"}`)
	commitSpec(t, e, proj, "SPEC.md", specV1, "seed")
	settleBaseline(t, e, proj, "s-046-16", "settle the installed example")

	e.Run(proj, "s-046-16b", "add a hook", Turns("done",
		Write("w1", ".claude/hooks/x.py", "# sr:invariant \"<the pin>\"\ndef run():\n    pass\n"),
	).ThenCommit("add the hook"))
	if blocks := e.BlockingErrorsFrom(proj, "s-046-16b", "Stop"); len(blocks) == 0 {
		t.Fatalf("a hook under .claude/hooks/ carrying a placeholder pin was not refused")
	}
}
