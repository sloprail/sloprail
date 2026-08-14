package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// HOLE 1 — the greedy verdict parse, in both engine-repo judges.
//
// Round 2 narrowed right-content-right-file's pattern from `{.*}` to
// `{[^{}]*}` and deliberately LEFT unit-satisfies-constraints greedy, because
// USC's verdict NESTS (`{"violations":[{...}]}`) and a narrowed pattern would
// take the first inner object and permit. That reasoning is sound for USC, and
// it was re-verified in this round.
//
// It does not transfer to these two. Both ask for the FLAT
// `{"has_issues":...,"reasoning":...}` shape — the same shape RCRF was narrowed
// for — while keeping the greedy pattern RCRF was narrowed away from. So a
// verdict file holding two objects yields a merged span, `jq` emits one line
// per object, `has_issues` becomes a two-line string, and the test
// `[ "$has_issues" != "true" ]` is satisfied by it. A flagged file is
// PERMITTED.
//
// The stub judge below is what makes this a deterministic reproduction rather
// than a race against a model's output discipline. The defect is in the
// SCRIPT's parse: given a verdict that flags the file, the script must refuse,
// and what the model had to do to produce two objects is a separate question
// about likelihood, not about whether the parse is correct.

// stubJudge writes a fixed verdict body to the path the prompt names, standing
// in for `claude` via A10N_CLAUDE_BIN.
//
// It reads the prompt on stdin and recovers the verdict path from it, exactly
// as the real judge does — so the isolation flags, the /tmp cwd and the path
// the script chose are all still exercised. Only the model's judgement is
// replaced.
func stubJudge(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	script := `#!/bin/sh
# Consume the prompt and recover the verdict path from it: the last /tmp/*.json
# token the prompt names. The real judge is told the path the same way.
prompt="$(cat)"
verdict="$(printf '%s' "$prompt" | tr ' ' '\n' | grep '^/tmp/.*\.json$' | tail -1)"
[ -n "$verdict" ] || exit 1
cat > "$verdict" <<'VERDICT_EOF'
` + body + `
VERDICT_EOF
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("stubJudge: write: %v", err)
	}
	return path
}

// TestR3_01_RuleQualityGreedyParsePermitsAFlaggedRule reproduces the hole in
// rule-quality.
//
// The verdict flags the rule. The judge must refuse. Before the fix it permits,
// because the greedy span swallows both objects.
func TestR3_01_RuleQualityGreedyParsePermitsAFlaggedRule(t *testing.T) {
	// Two objects, the JUDGE'S OWN ANSWER FIRST and a trailing echo of the
	// schema after it — the shape a model produces when it answers and then
	// restates the format it was given.
	//
	// This ordering is what makes the test decisive. The judge really did flag
	// the rule, so the only correct outcome is a refusal; greedy `{.*}` merges
	// both objects into a two-line `has_issues` that the `!= "true"` test
	// swallows, and the flagged rule is permitted.
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t,
		`{"has_issues": true, "reasoning": "the rule closes with a recap, violating high-signal"}
{"has_issues": false, "reasoning": ""}`))

	e := New(t)
	proj := project(t, e, "rule-quality")

	got := e.Run(proj, "s-r3-01", "write a rule", Turns("done",
		harness.Write("w1", "guardrails/x/rules/y/RULE.md", "# A rule\n\nBody that the judge flags.\n"),
	))

	if !got.Saw("RULE QUALITY") {
		t.Fatalf("a verdict flagging the rule did not refuse — the greedy parse permitted it:\n%s", got.Output)
	}
}

// TestR3_02_SkillQualityGreedyParsePermitsAFlaggedSkill is the same hole in the
// sibling. Present as its own test because the two scripts are separate files:
// a fix applied to one and not the other is exactly the failure this round is
// hunting, and one test covering both would not show it.
func TestR3_02_SkillQualityGreedyParsePermitsAFlaggedSkill(t *testing.T) {
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t,
		`{"has_issues": true, "reasoning": "the skill transcribes a flag table, violating help-is-not-skill"}
{"has_issues": false, "reasoning": ""}`))

	e := New(t)
	proj := project(t, e, "skill-quality")

	got := e.Run(proj, "s-r3-02", "write a skill", Turns("done",
		harness.Write("w1", "skills/x/SKILL.md", "# A skill\n\nBody that the judge flags.\n"),
	))

	if !got.Saw("SKILL QUALITY") {
		t.Fatalf("a verdict flagging the skill did not refuse — the greedy parse permitted it:\n%s", got.Output)
	}
}

// TestR3_03_RuleQualityStillPermitsACleanRule is the OTHER direction, and it is
// what keeps the fix from being "refuse everything".
//
// A single clean verdict object must still permit. Without this, narrowing the
// pattern to `{[^{}]*}` could be replaced by any change that refuses more, and
// the suite would call it a pass.
func TestR3_03_RuleQualityStillPermitsACleanRule(t *testing.T) {
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t, `{"has_issues": false, "reasoning": ""}`))

	e := New(t)
	proj := project(t, e, "rule-quality")

	got := e.Run(proj, "s-r3-03", "write a clean rule", Turns("done",
		harness.Write("w1", "guardrails/x/rules/y/RULE.md", "# A rule\n\nA clean body.\n"),
	))

	if got.Saw("RULE QUALITY") {
		t.Fatalf("a clean verdict refused:\n%s", got.Output)
	}
}

// TestR3_04_SkillQualityStillPermitsACleanSkill — the same other-direction
// proof for the sibling.
func TestR3_04_SkillQualityStillPermitsACleanSkill(t *testing.T) {
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t, `{"has_issues": false, "reasoning": ""}`))

	e := New(t)
	proj := project(t, e, "skill-quality")

	got := e.Run(proj, "s-r3-04", "write a clean skill", Turns("done",
		harness.Write("w1", "skills/x/SKILL.md", "# A skill\n\nA clean body.\n"),
	))

	if got.Saw("SKILL QUALITY") {
		t.Fatalf("a clean verdict refused:\n%s", got.Output)
	}
}

// TestR3_05_UnitSatisfiesConstraintsGreedyIsStillCorrect re-verifies the round-2
// reasoning that was deliberately NOT applied to USC, since this round is meant
// to check that the exception still holds rather than take it on trust.
//
// USC's verdict nests, so a merged span leaves `.violations` readable and the
// `[ -z "$lines" ]` test REFUSES on it — the fail-closed direction. Narrowing
// USC's pattern would take the first inner object, `.violations` would find
// nothing, and the unit would be permitted. The exception is correct.
//
// Asserted here as the pipeline itself rather than through a session, because
// USC lives in the owner's repo and this package installs from the engine's.
// The claim under test is about the two shell pipelines, and it is the
// DIFFERENCE between them that the round-2 note rests on.
func TestR3_05_UnitSatisfiesConstraintsGreedyIsStillCorrect(t *testing.T) {
	merged := `{"violations":[]} {"violations":[{"constraint":"c","level":"must","reasoning":"r"}]}`

	// Greedy, as USC has it: the span covers both, jq reads .violations off
	// each, and a non-empty line list results — which refuses.
	lines := shell(t, `printf '%s' `+shq(merged)+` | tr '\n' ' ' | grep -o '{.*}' | head -1 | jq -r '(.violations // []) | map("\(.constraint // "?") [\(.level // .moscow // "?")]: \(.reasoning // "")") | join("; ")'`)
	if lines == "" {
		t.Fatalf("USC's greedy pattern permitted a merged verdict — the round-2 exception no longer holds")
	}

	// Narrowed, as the siblings have it: the first inner object is taken, the
	// key naming it is gone, and .violations reads empty — which permits. This
	// is why USC was left greedy.
	narrow := shell(t, `printf '%s' `+shq(merged)+` | tr '\n' ' ' | grep -o '{[^{}]*}' | head -1 | jq -r '(.violations // []) | length'`)
	if narrow != "0" {
		t.Fatalf("narrowing USC would not have lost the violations after all, got %q — the recorded reasoning is wrong", narrow)
	}
}
