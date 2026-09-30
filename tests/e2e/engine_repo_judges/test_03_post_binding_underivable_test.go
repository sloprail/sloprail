package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// INVARIANT: the STOP file-guard judges a create the engine could not derive, and
// the GATE refuses to judge one it cannot see.
//
// A gate only sees a write whose resulting bytes the engine can derive
// (internal/filemod/module.go, KindPreCreate): a write whose output the engine
// will not guess reaches NO gate's judge with content. The plain file-guard covers
// exactly this: it fires on the settled POST file event at Stop, so a create the
// gate never saw is still judged. That is the file-guard's shape, but the shape is
// not the behaviour — what matters is whether the Stop check actually judges the
// file when no Pre content was available.
//
// Asserted through a real underivable write rather than read off the declaration:
// the mock runs a Bash command whose output bytes the engine will not predict, so
// no derivable Pre event is emitted for the file it creates, and the file-guard is
// the only thing that can still judge it. Only the judge's own model verdict is
// stubbed (InstallJudgeClaude), and the Post event carries the settled bytes in
// event.newContent (read off the tree diff), which the judge template reads the
// same way it reads a Pre write's.

// TestPostBindingJudgesAnUnderivableRuleCreate — the underivable-create case for
// rule-quality's file-guard.
//
// The rule is created by a command the engine cannot derive bytes for, so no gate
// can judge it. The Stop file-guard must still judge it and refuse.
func TestPostBindingJudgesAnUnderivableRuleCreate(t *testing.T) {
	e := New(t)
	proj := guardProject(t, e, "rule-quality")
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "RULE QUALITY: flagged via the file-guard"}`)

	e.Run(proj, "s-erj-post-rule", "make a rule the hard way", Turns("done",
		underivableWrite("RULE.md", "# A rule made by a command"),
	))

	// Read from the blocking-error channel, not the mock's stdout. A Stop
	// refusal cannot undo the write — the file is on disk and the cycle is
	// over — so it surfaces as a refusal that stops the TURN, which is the
	// mechanism by which an after-the-fact rule gets anything corrected.
	// Asserting on stdout would look for a prevention this timing never
	// performs, and would fail against a perfectly correct engine.
	if !sawRefusal(e.BlockingErrors(proj, "s-erj-post-rule"), "RULE QUALITY") {
		t.Fatalf("an underivable create was never judged — the file-guard did not cover what the gate could not see:\n%v", e.BlockingErrors(proj, "s-erj-post-rule"))
	}
}

// TestPostBindingJudgesAnUnderivableSkillCreate — the same for the sibling.
func TestPostBindingJudgesAnUnderivableSkillCreate(t *testing.T) {
	e := New(t)
	proj := guardProject(t, e, "skill-quality")
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SKILL QUALITY: flagged via the file-guard"}`)

	e.Run(proj, "s-erj-post-skill", "make a skill the hard way", Turns("done",
		underivableWrite("SKILL.md", "# A skill made by a command"),
	))

	if !sawRefusal(e.BlockingErrors(proj, "s-erj-post-skill"), "SKILL QUALITY") {
		t.Fatalf("an underivable create was never judged — the file-guard did not cover what the gate could not see:\n%v", e.BlockingErrors(proj, "s-erj-post-skill"))
	}
}

// TestGateRefusesAWriteItCannotDerive: the gate does not fail closed on an
// underivable write by itself, so prepare.sh refuses it (resultKnown false)
// rather than judging an empty newContent. A passing verdict is stubbed: were the
// gate to judge the empty content it would ADMIT, so a refusal proves the
// resultKnown check. sed -i on an existing, committed RULE.md is the underivable
// update.
func TestGateRefusesAWriteItCannotDerive(t *testing.T) {
	e := New(t)
	proj := project(t, e, "rule-quality")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
	e.WriteFile(proj, "RULE.md", "# A rule\n\nOld body.\n")
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "add a rule")

	got := e.Run(proj, "s-erj-gate-underivable", "edit the rule in place", Turns("done",
		harness.Bash("w1", "sed -i.bak 's/Old/New/' RULE.md"),
	))

	if !got.Saw("resultKnown false") {
		t.Fatalf("the gate did not refuse a write whose result it could not derive:\n%s", got.Output)
	}
}
