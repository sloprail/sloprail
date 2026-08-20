package e2e

import (
	"testing"
)

// INVARIANT: the POST binding judges a create the engine could not derive.
//
// A guardrail that bound only the Pre kinds would miss a creation the engine
// cannot predict the bytes of — the engine emits a create only when the resulting
// bytes are known (internal/filemod/module.go, KindPreCreate), so a write whose
// output the engine will not guess reaches NO Pre kind at all. Both engine-repo
// judges DECLARE all four kinds; that is the fix's shape, but a declaration is not
// the behaviour. What matters is whether the Post binding actually judges the file
// when the Pre kind never fired.
//
// Asserted through a real underivable write rather than read off the frontmatter:
// the mock runs a Bash command whose output bytes the engine will not predict, so
// no Pre event is emitted for the file it creates, and the Post binding is the only
// thing that can still judge it. Only the judge's own model verdict is stubbed.

// TestPostBindingJudgesAnUnderivableRuleCreate — the underivable-create case for
// rule-quality.
//
// The rule is created by a command the engine cannot derive bytes for, so the
// Pre kind never fires. The Post binding must still judge it and refuse.
func TestPostBindingJudgesAnUnderivableRuleCreate(t *testing.T) {
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t,
		`{"has_issues": true, "reasoning": "flagged via the Post binding"}`))

	e := New(t)
	proj := project(t, e, "rule-quality")

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
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t,
		`{"has_issues": true, "reasoning": "flagged via the Post binding"}`))

	e := New(t)
	proj := project(t, e, "skill-quality")

	e.Run(proj, "s-erj-post-skill", "make a skill the hard way", Turns("done",
		underivableWrite("SKILL.md", "# A skill made by a command"),
	))

	if !sawRefusal(e.BlockingErrors(proj, "s-erj-post-skill"), "SKILL QUALITY") {
		t.Fatalf("an underivable create was never judged — the Post binding did not cover what the Pre kind could not see:\n%v", e.BlockingErrors(proj, "s-erj-post-skill"))
	}
}
