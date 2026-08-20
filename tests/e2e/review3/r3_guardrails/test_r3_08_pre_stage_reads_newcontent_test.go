package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The PRE stage reads the PENDING body, and reads it from the right field.
//
// Both engine-repo judges bind PreFileCreate/PreFileUpdate as well as the Post
// kinds, and the Pre binding is the one that PREVENTS a bad SKILL.md/RULE.md
// from landing rather than reporting it after the fact. It works only if the
// hook reads the pending body out of the event — which the file kinds carry as
// `newContent` (the field the events-vocab rename settled on). A hook still
// reading the old `.event.fields.content` gets an empty body, hits
// `[ -n "$body" ] || exit 0`, and PERMITS — the Pre stage silently dead while
// the Post stage still fires off disk.
//
// These tests pin the Pre behaviour directly: a Write TOOL (which lets the
// engine derive the pending bytes, so the Pre kind fires) creating a flagged
// file must be REFUSED. That refusal is only possible if the hook read the
// content the write states, so the assertion is a proof the field is read
// correctly — the exact regression the rename could have left behind, and did
// until judge-skill.sh/judge-rule.sh were brought to `newContent`.
//
// Distinct from R3_01/02, whose subject is the greedy verdict PARSE: those also
// happen to exercise the Pre stage, but their name and their failure mode are
// about the parse, so a reader hunting a dead Pre binding would not find the
// guarantee there. This states it as its own claim.

// TestR3_08_SkillQualityPreStageRefusesAFlaggedWrite: a Write creating a flagged
// SKILL.md is refused BEFORE it lands.
func TestR3_08_SkillQualityPreStageRefusesAFlaggedWrite(t *testing.T) {
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t,
		`{"has_issues": true, "reasoning": "flagged at the Pre stage"}`))

	e := New(t)
	proj := project(t, e, "skill-quality")

	got := e.Run(proj, "s-r3-08", "write a bad skill with a tool", Turns("done",
		harness.Write("w1", "skills/x/SKILL.md", "# A skill\n\nA body the judge flags.\n"),
	))

	// A Pre refusal reaches the agent on the tool-call channel, so the mock's
	// own stream carries it — unlike a Post refusal, which surfaces as a
	// blocking error. Asserting on the stream is therefore correct here and is
	// what distinguishes a prevented write from an after-the-fact objection.
	if !got.Saw("SKILL QUALITY") {
		t.Fatalf("a Write creating a flagged SKILL.md was not refused at the Pre stage — "+
			"the hook did not read the pending body (newContent), so the Pre binding is silently dead:\n%s", got.Output)
	}
}

// TestR3_09_RuleQualityPreStageRefusesAFlaggedWrite: the same for the sibling.
// Its own test because the two judges are separate files and a rename fixed in
// one and missed in the other is exactly the half-migration this round hunts.
func TestR3_09_RuleQualityPreStageRefusesAFlaggedWrite(t *testing.T) {
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t,
		`{"has_issues": true, "reasoning": "flagged at the Pre stage"}`))

	e := New(t)
	proj := project(t, e, "rule-quality")

	got := e.Run(proj, "s-r3-09", "write a bad rule with a tool", Turns("done",
		harness.Write("w1", "guardrails/x/rules/y/RULE.md", "# A rule\n\nA body the judge flags.\n"),
	))

	if !got.Saw("RULE QUALITY") {
		t.Fatalf("a Write creating a flagged RULE.md was not refused at the Pre stage — "+
			"the hook did not read the pending body (newContent), so the Pre binding is silently dead:\n%s", got.Output)
	}
}

// TestR3_10_SkillQualityPreStageStillPermitsACleanWrite is the other-direction
// control: a clean SKILL.md written by a tool must NOT be refused, so the tests
// above cannot pass by an engine that refuses every Pre write.
func TestR3_10_SkillQualityPreStageStillPermitsACleanWrite(t *testing.T) {
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t, `{"has_issues": false, "reasoning": ""}`))

	e := New(t)
	proj := project(t, e, "skill-quality")

	got := e.Run(proj, "s-r3-10", "write a clean skill with a tool", Turns("done",
		harness.Write("w1", "skills/x/SKILL.md", "# A skill\n\nA clean body.\n"),
	))

	if got.Saw("SKILL QUALITY") {
		t.Fatalf("a clean SKILL.md written at the Pre stage was refused:\n%s", got.Output)
	}
}
