package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// INVARIANT: the judge reads the SETTLED body, from the right field.
//
// Both engine-repo judges live in a plain FILE-GUARD (a judge is a model call, so
// it rules on the result at Stop, not before every write). It works only if the
// judge reads the settled body out of the flat event — which a Post file kind
// carries as `event.newContent`, the field the judge TEMPLATE interpolates. A
// template reading the wrong field would render an empty file, the model would find
// nothing to flag, and the rule would be silently dead.
//
// A Write TOOL creates a flagged file; the write lands (a file-guard does not
// prevent), and the turn is BLOCKED at Stop with the judge's reasoning. That is only
// possible if the judge saw the content the write states.
//
// The whole path runs through the mock; the only substitution is the judge's own
// model verdict (InstallJudgeClaude).

// TestStopJudgeBlocksAFlaggedSkillWrite: a Write creating a flagged SKILL.md lands,
// and the Stop file-guard blocks the turn.
func TestStopJudgeBlocksAFlaggedSkillWrite(t *testing.T) {
	e := New(t)
	proj := guardProject(t, e, "skill-quality")
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SKILL QUALITY: flagged at Stop"}`)

	e.Run(proj, "s-erj-pre-skill", "write a bad skill with a tool", Turns("done",
		harness.Write("w1", "skills/x/SKILL.md", "# A skill\n\nA body the judge flags.\n"),
	))

	if !e.Exists(proj, "skills/x/SKILL.md") {
		t.Errorf("the write did not land: a file-guard does not prevent")
	}
	if !sawRefusal(e.BlockingErrors(proj, "s-erj-pre-skill"), "SKILL QUALITY") {
		t.Fatalf("a flagged SKILL.md was not blocked at Stop — the judge did not see the settled body (event.newContent):\n%v", e.BlockingErrors(proj, "s-erj-pre-skill"))
	}
}

// TestStopJudgeBlocksAFlaggedRuleWrite: the same for the sibling, its own test
// because the two judges are separate files.
func TestStopJudgeBlocksAFlaggedRuleWrite(t *testing.T) {
	e := New(t)
	proj := guardProject(t, e, "rule-quality")
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "RULE QUALITY: flagged at Stop"}`)

	e.Run(proj, "s-erj-pre-rule", "write a bad rule with a tool", Turns("done",
		harness.Write("w1", "guardrails/x/rules/y/RULE.md", "# A rule\n\nA body the judge flags.\n"),
	))

	if !sawRefusal(e.BlockingErrors(proj, "s-erj-pre-rule"), "RULE QUALITY") {
		t.Fatalf("a flagged RULE.md was not blocked at Stop — the judge did not see the settled body (event.newContent):\n%v", e.BlockingErrors(proj, "s-erj-pre-rule"))
	}
}

// TestStopJudgePermitsACleanSkillWrite is the other-direction control, so the
// tests above cannot pass by an engine that blocks every turn.
func TestStopJudgePermitsACleanSkillWrite(t *testing.T) {
	e := New(t)
	proj := guardProject(t, e, "skill-quality")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-erj-pre-clean", "write a clean skill with a tool", Turns("done",
		harness.Write("w1", "skills/x/SKILL.md", "# A skill\n\nA clean body.\n"),
	))

	if sawRefusal(e.BlockingErrors(proj, "s-erj-pre-clean"), "SKILL QUALITY") {
		t.Fatalf("a clean SKILL.md was blocked at Stop")
	}
}
