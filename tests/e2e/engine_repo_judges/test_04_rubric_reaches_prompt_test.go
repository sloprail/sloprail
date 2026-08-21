package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// INVARIANT: the ASSEMBLED RUBRIC reaches the judge's prompt.
//
// The migration split the old script into a prepare.sh (which assembles the
// rubric from the guard's rules/ directory) and a judge template (which renders
// that rubric plus the file content). A stubbed verdict alone cannot prove the
// prepare's output reached the template: the renderer treats an undefined variable
// as empty, so `{{ additionalContext.rubric }}` renders fine whether prepare
// produced it or nothing (internal/dispatch, TestTemplate_UndefinedIsEmptyAndFalsy).
// A test that only flipped the verdict would pass against an engine that never ran
// prepare and never spliced the meta-rules in.
//
// So these capture the rendered prompt and assert two things a rubric that was
// really assembled must carry: the ENFORCED meta-rule's own text (proving prepare
// read rules/ and the template interpolated it), AND the file's own content
// (proving event.newContent reached the same prompt). Both present means the whole
// prepare -> template wiring is live — the exact thing the judge-check migration
// could have broken.
//
// The assertion targets a phrase from the shipped enforced meta-rule BODY rather
// than restating it, so it tracks whatever the guard actually enforces. A passing
// verdict is stubbed, so the point is the PROMPT, not the block.

// distinctivePhraseHighSignal is a phrase from the BODY of the high-signal
// meta-rule — the enforced one both guards ship ("A rule/skill is the shortest
// text that still carries its meaning") — and it appears nowhere in the RUBRIC
// frame or the test's own file content, so finding it in the prompt can only mean
// the enforced meta-rule's body was spliced in. The shared tail is what both
// guards' high-signal rules have in common, so one const covers both.
const distinctivePhraseHighSignal = "shortest text that still carries its meaning"

// TestRubricReachesRuleJudgePrompt: for rule-quality, the assembled rubric (the
// enforced meta-rule) AND the file content both reach the judge's prompt.
func TestRubricReachesRuleJudgePrompt(t *testing.T) {
	e := New(t)
	proj := project(t, e, "rule-quality")
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	const marker = "ZZ_RULE_BODY_MARKER a distinctive line in the rule body"
	e.Run(proj, "s-erj-wire-rule", "write a rule", Turns("done",
		harness.Write("w1", "guardrails/x/rules/y/RULE.md", "# A rule\n\n"+marker+"\n"),
	))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the rule-quality judge never ran — no prompt captured (did prepare pass and the judge run?)")
	}
	// The file content: present only if event.newContent reached the template.
	if !strings.Contains(prompt, marker) {
		t.Fatalf("the rule's own content did not reach the judge prompt — event.newContent wiring is broken:\n%s", prompt)
	}
	// The assembled rubric: present only if prepare read rules/ AND the template
	// rendered additionalContext.rubric. The enforced meta-rule's own phrase is the
	// proof it was spliced in, not just the frame.
	if !strings.Contains(prompt, distinctivePhraseHighSignal) {
		t.Fatalf("the assembled rubric (the enforced meta-rule) did not reach the judge prompt — prepare/template wiring is broken:\n%s", prompt)
	}
}

// TestRubricReachesSkillJudgePrompt: the same for skill-quality.
func TestRubricReachesSkillJudgePrompt(t *testing.T) {
	e := New(t)
	proj := project(t, e, "skill-quality")
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	const marker = "ZZ_SKILL_BODY_MARKER a distinctive line in the skill body"
	e.Run(proj, "s-erj-wire-skill", "write a skill", Turns("done",
		harness.Write("w1", "skills/x/SKILL.md", "# A skill\n\n"+marker+"\n"),
	))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the skill-quality judge never ran — no prompt captured (did prepare pass and the judge run?)")
	}
	if !strings.Contains(prompt, marker) {
		t.Fatalf("the skill's own content did not reach the judge prompt — event.newContent wiring is broken:\n%s", prompt)
	}
	if !strings.Contains(prompt, distinctivePhraseHighSignal) {
		t.Fatalf("the assembled rubric (the enforced meta-rule) did not reach the judge prompt — prepare/template wiring is broken:\n%s", prompt)
	}
}

// TestEmptyRulesIsRefusalNotFailOpen pins the PRESERVED asymmetry: a guard whose
// rules/ has no enforced meta-rule must REFUSE (not permit), even though every
// other machinery failure's old fail-open became a refusal too. This installs the
// guard, then strips its rules/ down to nothing enforced, and asserts the write is
// refused with prepare's own "no standard to judge" message — the one fail-CLOSED
// that was always fail-closed, unchanged by the migration.
func TestEmptyRulesIsRefusalNotFailOpen(t *testing.T) {
	e := New(t)
	proj := project(t, e, "rule-quality")
	// A passing verdict, so if the guard wrongly reached the judge it would ADMIT —
	// the test would then fail, which is what makes the refusal meaningful.
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	// Neuter the one enforced meta-rule by flipping its flag, so rules/ has zero
	// enforced rules — the empty-standard case prepare refuses on.
	e.WriteFile(proj, ".sloprail/file-guard/rule-quality/rules/high-signal/RULE.md",
		"---\nenforced: false\n---\n# high-signal\n\nno longer enforced\n")
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "disable the only enforced meta-rule")

	got := e.Run(proj, "s-erj-emptyrules", "write a rule", Turns("done",
		harness.Write("w1", "guardrails/x/rules/y/RULE.md", "# A rule\n\nA body.\n"),
	))

	// The preventive Pre refusal reaches the mock's stream. prepare refuses with
	// its "no standard to judge" message rather than permitting — the asymmetry.
	if !got.Saw("no standard to judge") {
		t.Fatalf("an empty (no-enforced-rule) rules/ did not refuse — the empty-rules asymmetry was lost:\n%s", got.Output)
	}
}
