package e2e

import (
	"testing"
)

// INVARIANT: the POST after-check judges a create the engine could not derive.
//
// A guard that only prevented at the Pre kinds would miss a creation the engine
// cannot predict the bytes of — the engine emits a create only when the resulting
// bytes are known (internal/filemod/module.go, KindPreCreate), so a write whose
// output the engine will not guess reaches NO Pre kind at all. A file-guard's
// after-check covers exactly this: EVERY file-guard (preventive or not) fires on
// the settled POST file event at Stop, so a create the preventive Pre run never
// saw is still judged. That is the file-guard's shape, but the shape is not the
// behaviour — what matters is whether the after-check actually judges the file
// when the Pre kind never fired.
//
// Asserted through a real underivable write rather than read off the declaration:
// the mock runs a Bash command whose output bytes the engine will not predict, so
// no Pre event is emitted for the file it creates, and the Post after-check is the
// only thing that can still judge it. Only the judge's own model verdict is
// stubbed (InstallJudgeClaude), and the Post event carries the settled bytes in
// event.newContent (read off the tree diff), which the judge template reads the
// same way it reads a Pre write's.

// TestPostBindingJudgesAnUnderivableRuleCreate — the underivable-create case for
// rule-quality.
//
// The rule is created by a command the engine cannot derive bytes for, so the
// preventive Pre run never fires. The Post after-check must still judge it and
// refuse.
func TestPostBindingJudgesAnUnderivableRuleCreate(t *testing.T) {
	e := New(t)
	proj := project(t, e, "rule-quality")
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "RULE QUALITY: flagged via the Post binding"}`)

	e.Run(proj, "s-erj-post-rule", "make a rule the hard way", Turns("done",
		underivableWrite("RULE.md", "# A rule made by a command"),
	))

	// Read from the blocking-error channel, not the mock's stdout. A Post
	// refusal cannot undo the write — the file is on disk and the cycle is
	// over — so it surfaces as a refusal that stops the TURN, which is the
	// mechanism by which an after-the-fact rule gets anything corrected.
	// Asserting on stdout would look for a prevention this timing never
	// performs, and would fail against a perfectly correct engine.
	if !sawRefusal(e.BlockingErrors(proj, "s-erj-post-rule"), "RULE QUALITY") {
		t.Fatalf("an underivable create was never judged — the Post binding did not cover what the Pre kind could not see:\n%v", e.BlockingErrors(proj, "s-erj-post-rule"))
	}
}

// TestPostBindingJudgesAnUnderivableSkillCreate — the same for the sibling.
func TestPostBindingJudgesAnUnderivableSkillCreate(t *testing.T) {
	e := New(t)
	proj := project(t, e, "skill-quality")
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SKILL QUALITY: flagged via the Post binding"}`)

	e.Run(proj, "s-erj-post-skill", "make a skill the hard way", Turns("done",
		underivableWrite("SKILL.md", "# A skill made by a command"),
	))

	if !sawRefusal(e.BlockingErrors(proj, "s-erj-post-skill"), "SKILL QUALITY") {
		t.Fatalf("an underivable create was never judged — the Post binding did not cover what the Pre kind could not see:\n%v", e.BlockingErrors(proj, "s-erj-post-skill"))
	}
}
