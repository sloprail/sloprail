package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// INVARIANT: the verdict parse refuses a FLAGGED verdict, even one carrying more
// than one JSON object.
//
// Both engine-repo judges ask for the FLAT `{"has_issues":...,"reasoning":...}`
// shape and read it with the narrow `{[^{}]*}` pattern — the first brace-delimited
// run with no inner braces. A greedy `{.*}` is a hole: given two objects on one
// line it returns the span from the first `{` to the LAST `}`, jq emits one line
// per object, `has_issues` becomes a two-line string, and `[ "$has_issues" != "true" ]`
// is satisfied by it — so a verdict that flags the file PERMITS it.
//
// The stub judge is what makes this a deterministic reproduction rather than a
// race against a model's output discipline: the defect is in the SCRIPT's parse,
// so given a verdict that flags the file the script must refuse, and what a model
// had to do to produce two objects is a separate question about likelihood. The
// whole path is the mock's — the write is attempted through a10n-claude-mock and
// the real guardrail fires; only the verdict text is fixed.

// TestVerdictParseRefusesAFlaggedRule reproduces the hole in rule-quality: the
// verdict flags the rule, so the judge must refuse; a greedy span would swallow
// both objects and permit.
func TestVerdictParseRefusesAFlaggedRule(t *testing.T) {
	// Two objects, the JUDGE'S OWN ANSWER FIRST and a trailing echo of the
	// schema after it — the shape a model produces when it answers and then
	// restates the format it was given. This ordering is what makes the test
	// decisive: the judge really did flag the rule, so the only correct outcome
	// is a refusal.
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t,
		`{"has_issues": true, "reasoning": "the rule closes with a recap, violating high-signal"}
{"has_issues": false, "reasoning": ""}`))

	e := New(t)
	proj := project(t, e, "rule-quality")

	got := e.Run(proj, "s-erj-parse-rule", "write a rule", Turns("done",
		harness.Write("w1", "guardrails/x/rules/y/RULE.md", "# A rule\n\nBody that the judge flags.\n"),
	))

	if !got.Saw("RULE QUALITY") {
		t.Fatalf("a verdict flagging the rule did not refuse — the greedy parse permitted it:\n%s", got.Output)
	}
}

// TestVerdictParseRefusesAFlaggedSkill is the same hole in the sibling. Present
// as its own test because the two scripts are separate files: a fix applied to
// one and not the other is exactly the failure this hunts, and one test covering
// both would not show it.
func TestVerdictParseRefusesAFlaggedSkill(t *testing.T) {
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t,
		`{"has_issues": true, "reasoning": "the skill transcribes a flag table, violating help-is-not-skill"}
{"has_issues": false, "reasoning": ""}`))

	e := New(t)
	proj := project(t, e, "skill-quality")

	got := e.Run(proj, "s-erj-parse-skill", "write a skill", Turns("done",
		harness.Write("w1", "skills/x/SKILL.md", "# A skill\n\nBody that the judge flags.\n"),
	))

	if !got.Saw("SKILL QUALITY") {
		t.Fatalf("a verdict flagging the skill did not refuse — the greedy parse permitted it:\n%s", got.Output)
	}
}

// TestVerdictParsePermitsACleanRule is the OTHER direction, and it is what keeps
// the fix from being "refuse everything".
//
// A single clean verdict object must still permit. Without this, narrowing the
// pattern to `{[^{}]*}` could be replaced by any change that refuses more, and
// the suite would call it a pass.
func TestVerdictParsePermitsACleanRule(t *testing.T) {
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t, `{"has_issues": false, "reasoning": ""}`))

	e := New(t)
	proj := project(t, e, "rule-quality")

	got := e.Run(proj, "s-erj-clean-rule", "write a clean rule", Turns("done",
		harness.Write("w1", "guardrails/x/rules/y/RULE.md", "# A rule\n\nA clean body.\n"),
	))

	if got.Saw("RULE QUALITY") {
		t.Fatalf("a clean verdict refused:\n%s", got.Output)
	}
}

// TestVerdictParsePermitsACleanSkill — the same other-direction proof for the
// sibling.
func TestVerdictParsePermitsACleanSkill(t *testing.T) {
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t, `{"has_issues": false, "reasoning": ""}`))

	e := New(t)
	proj := project(t, e, "skill-quality")

	got := e.Run(proj, "s-erj-clean-skill", "write a clean skill", Turns("done",
		harness.Write("w1", "skills/x/SKILL.md", "# A skill\n\nA clean body.\n"),
	))

	if got.Saw("SKILL QUALITY") {
		t.Fatalf("a clean verdict refused:\n%s", got.Output)
	}
}

// TestUnitSatisfiesConstraintsGreedyIsStillCorrect re-verifies the reasoning that
// was deliberately NOT applied to the sibling unit-satisfies-constraints judge
// (which lives in the owner's own repo, not this one), so a later "consistency"
// change cannot quietly narrow it and open the hole the narrow pattern closes
// elsewhere.
//
// USC's verdict NESTS (`{"violations":[{...}]}`), so a merged span leaves
// `.violations` readable and the `[ -z "$lines" ]` test REFUSES on it — the
// fail-closed direction. Narrowing USC's pattern would take the first inner
// object, `.violations` would find nothing, and the unit would be permitted. So
// the correct pattern is decided by the verdict's shape, not copied between rules.
//
// Asserted as the two shell pipelines themselves rather than through a session,
// because USC lives in the owner's repo and this package installs from the
// engine's — the claim under test is the DIFFERENCE between the two pipelines.
func TestUnitSatisfiesConstraintsGreedyIsStillCorrect(t *testing.T) {
	merged := `{"violations":[]} {"violations":[{"constraint":"c","level":"must","reasoning":"r"}]}`

	// Greedy, as USC has it: the span covers both, jq reads .violations off
	// each, and a non-empty line list results — which refuses.
	lines := shell(t, `printf '%s' `+shq(merged)+` | tr '\n' ' ' | grep -o '{.*}' | head -1 | jq -r '(.violations // []) | map("\(.constraint // "?") [\(.level // .moscow // "?")]: \(.reasoning // "")") | join("; ")'`)
	if lines == "" {
		t.Fatalf("USC's greedy pattern permitted a merged verdict — the exception no longer holds")
	}

	// Narrowed, as the siblings have it: the first inner object is taken, the
	// key naming it is gone, and .violations reads empty — which permits. This
	// is why USC was left greedy.
	narrow := shell(t, `printf '%s' `+shq(merged)+` | tr '\n' ' ' | grep -o '{[^{}]*}' | head -1 | jq -r '(.violations // []) | length'`)
	if narrow != "0" {
		t.Fatalf("narrowing USC would not have lost the violations after all, got %q — the recorded reasoning is wrong", narrow)
	}
}
