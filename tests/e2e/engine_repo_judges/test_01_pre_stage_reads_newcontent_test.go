package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// INVARIANT: the PRE stage reads the PENDING body, from the right field.
//
// Both engine-repo judges have a GATE on PreFileWrite (a file-guard acts only at
// Stop), and the gate is the one that PREVENTS a bad SKILL.md/RULE.md from landing
// rather than reporting it after the fact. It works only if the judge reads the
// pending body out of the flat event — which the file kinds carry as
// `event.newContent`, the field the judge TEMPLATE interpolates. A template
// reading the wrong field would render an empty file, the model would find
// nothing to flag, and the gate would be silently dead while the Stop
// file-guard still fires off disk.
//
// These tests pin the gate's behaviour directly: a Write TOOL (which lets the
// engine derive the pending bytes, so the gate can decide) creating a flagged
// file must be REFUSED. That refusal is only
// possible if the judge saw the content the write states, so the assertion is a
// proof the field reaches the prompt — the exact regression the flat-event
// migration could have left behind.
//
// The whole path runs through the mock: the harness drives a10n-claude-mock to
// attempt the write, the real plugin fires this repo's real gate, prepare
// assembles the rubric, sr-agent runs the judge, and the only substitution is the
// judge's own model verdict (InstallJudgeClaude). Distinct from the verdict-PARSE
// invariant, whose subject is the greedy span rather than the stage — those also
// exercise the Pre stage but are about a different failure.

// TestPreStageRefusesAFlaggedSkillWrite: a Write creating a flagged SKILL.md is
// refused BEFORE it lands, which is only possible if the judge saw the pending
// bytes via event.newContent.
func TestPreStageRefusesAFlaggedSkillWrite(t *testing.T) {
	e := New(t)
	proj := project(t, e, "skill-quality")
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SKILL QUALITY: flagged at the Pre stage"}`)

	got := e.Run(proj, "s-erj-pre-skill", "write a bad skill with a tool", Turns("done",
		harness.Write("w1", "skills/x/SKILL.md", "# A skill\n\nA body the judge flags.\n"),
	))

	// A gate refusal reaches the agent on the tool-call channel, so the mock's
	// own stream carries it — unlike a Post refusal, which surfaces as a
	// blocking error. Asserting on the stream is therefore correct here and is
	// what distinguishes a prevented write from an after-the-fact objection.
	if !got.Saw("SKILL QUALITY") {
		t.Fatalf("a Write creating a flagged SKILL.md was not refused at the Pre stage — "+
			"the judge did not see the pending body (event.newContent), so the gate is silently dead:\n%s", got.Output)
	}
}

// TestPreStageRefusesAFlaggedRuleWrite: the same for the sibling. Its own test
// because the two judges are separate files and a fix in one and a miss in the
// other is exactly the half-migration this invariant guards against.
func TestPreStageRefusesAFlaggedRuleWrite(t *testing.T) {
	e := New(t)
	proj := project(t, e, "rule-quality")
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "RULE QUALITY: flagged at the Pre stage"}`)

	got := e.Run(proj, "s-erj-pre-rule", "write a bad rule with a tool", Turns("done",
		harness.Write("w1", "guardrails/x/rules/y/RULE.md", "# A rule\n\nA body the judge flags.\n"),
	))

	if !got.Saw("RULE QUALITY") {
		t.Fatalf("a Write creating a flagged RULE.md was not refused at the Pre stage — "+
			"the judge did not see the pending body (event.newContent), so the gate is silently dead:\n%s", got.Output)
	}
}

// TestPreStagePermitsACleanSkillWrite is the other-direction control: a clean
// SKILL.md written by a tool must NOT be refused, so the refusal tests above
// cannot pass by an engine that refuses every Pre write.
func TestPreStagePermitsACleanSkillWrite(t *testing.T) {
	e := New(t)
	proj := project(t, e, "skill-quality")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	got := e.Run(proj, "s-erj-pre-clean", "write a clean skill with a tool", Turns("done",
		harness.Write("w1", "skills/x/SKILL.md", "# A skill\n\nA clean body.\n"),
	))

	if got.Saw("SKILL QUALITY") {
		t.Fatalf("a clean SKILL.md written at the Pre stage was refused:\n%s", got.Output)
	}
}
