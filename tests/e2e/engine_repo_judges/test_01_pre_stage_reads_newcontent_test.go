package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// INVARIANT: the PRE stage reads the PENDING body, from the right field.
//
// Both engine-repo judges are `preventive: true` file-guards, so they fire at
// the PRE write as well as the after-check at Stop, and the preventive Pre run
// is the one that PREVENTS a bad SKILL.md/RULE.md from landing rather than
// reporting it after the fact. It works only if the check reads the pending body
// out of the flat CheckPayload event — which the file kinds carry as
// `event.newContent`. A check still reading the old nested
// `.event.fields.newContent` gets an empty body, hits
// `[ -n "$body" ] || exit 0`, and PERMITS — the Pre stage silently dead while
// the Post after-check still fires off disk.
//
// These tests pin the Pre behaviour directly: a Write TOOL (which lets the
// engine derive the pending bytes, so the preventive guard's Pre run fires and
// can block) creating a flagged file must be REFUSED. That refusal is only
// possible if the check read the content the write states, so the assertion is a
// proof the field is read correctly — the exact regression the flat-event
// migration could have left behind if a `.event.fields.*` read survived.
//
// The whole path runs through the mock: the harness drives a10n-claude-mock to
// attempt the write, the real plugin fires this repo's real file-guard, and the
// only substitution is the judge's own model verdict (stubJudge). Distinct from
// the verdict-PARSE invariant, whose subject is the greedy span rather than the
// stage — those also exercise the Pre stage but are about a different failure.

// TestPreStageRefusesAFlaggedSkillWrite: a Write creating a flagged SKILL.md is
// refused BEFORE it lands, which is only possible if judge-skill.sh read the
// pending bytes out of newContent.
func TestPreStageRefusesAFlaggedSkillWrite(t *testing.T) {
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t,
		`{"has_issues": true, "reasoning": "flagged at the Pre stage"}`))

	e := New(t)
	proj := project(t, e, "skill-quality")

	got := e.Run(proj, "s-erj-pre-skill", "write a bad skill with a tool", Turns("done",
		harness.Write("w1", "skills/x/SKILL.md", "# A skill\n\nA body the judge flags.\n"),
	))

	// A Pre refusal reaches the agent on the tool-call channel, so the mock's
	// own stream carries it — unlike a Post refusal, which surfaces as a
	// blocking error. Asserting on the stream is therefore correct here and is
	// what distinguishes a prevented write from an after-the-fact objection.
	if !got.Saw("SKILL QUALITY") {
		t.Fatalf("a Write creating a flagged SKILL.md was not refused at the Pre stage — "+
			"the check did not read the pending body (event.newContent), so the preventive Pre run is silently dead:\n%s", got.Output)
	}
}

// TestPreStageRefusesAFlaggedRuleWrite: the same for the sibling. Its own test
// because the two judges are separate files and a rename fixed in one and missed
// in the other is exactly the half-migration this invariant guards against.
func TestPreStageRefusesAFlaggedRuleWrite(t *testing.T) {
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t,
		`{"has_issues": true, "reasoning": "flagged at the Pre stage"}`))

	e := New(t)
	proj := project(t, e, "rule-quality")

	got := e.Run(proj, "s-erj-pre-rule", "write a bad rule with a tool", Turns("done",
		harness.Write("w1", "guardrails/x/rules/y/RULE.md", "# A rule\n\nA body the judge flags.\n"),
	))

	if !got.Saw("RULE QUALITY") {
		t.Fatalf("a Write creating a flagged RULE.md was not refused at the Pre stage — "+
			"the check did not read the pending body (event.newContent), so the preventive Pre run is silently dead:\n%s", got.Output)
	}
}

// TestPreStagePermitsACleanSkillWrite is the other-direction control: a clean
// SKILL.md written by a tool must NOT be refused, so the refusal tests above
// cannot pass by an engine that refuses every Pre write.
func TestPreStagePermitsACleanSkillWrite(t *testing.T) {
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t, `{"has_issues": false, "reasoning": ""}`))

	e := New(t)
	proj := project(t, e, "skill-quality")

	got := e.Run(proj, "s-erj-pre-clean", "write a clean skill with a tool", Turns("done",
		harness.Write("w1", "skills/x/SKILL.md", "# A skill\n\nA clean body.\n"),
	))

	if got.Saw("SKILL QUALITY") {
		t.Fatalf("a clean SKILL.md written at the Pre stage was refused:\n%s", got.Output)
	}
}
