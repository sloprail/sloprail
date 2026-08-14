package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// H5's TWIN-HUNT, for the two engine-repo judges.
//
// H5 was: require-skill bound Pre kinds only, so a creation the engine could
// not derive produced no event and the rule never ran. The engine states the
// premise directly — internal/filemod/module.go, KindPreCreate: "a create is
// emitted only when the resulting bytes are known" — so a write whose bytes the
// engine will not guess reaches NO Pre kind at all.
//
// Both new judges DO declare all four kinds. That is the fix's shape, but a
// declaration is not the behaviour: what matters is whether the Post binding
// actually judges the file when the Pre kind never fired. Asserted here through
// a real underivable write rather than read off the frontmatter.

// underivableWrite is a shell command whose output bytes the engine will not
// predict, so no Pre event is emitted for the file it creates. This is the
// exact tier H5 escaped through.
func underivableWrite(path, body string) harness.Turn {
	return harness.Bash("w1", "printf '%s\\n' "+shqLit(body)+" | tr -d '\\r' > "+path)
}

// sawRefusal reports whether any recorded refusal carries the given text.
func sawRefusal(errs []string, want string) bool {
	for _, e := range errs {
		if strings.Contains(e, want) {
			return true
		}
	}
	return false
}

// shqLit single-quotes for the shell, for embedding in a Bash turn.
func shqLit(s string) string {
	out := "'"
	for _, r := range s {
		if r == '\'' {
			out += `'\''`
			continue
		}
		out += string(r)
	}
	return out + "'"
}

// TestR3_06_RuleQualityJudgesAnUnderivableCreate — the H5 twin for rule-quality.
//
// The rule is created by a command the engine cannot derive bytes for, so the
// Pre kind never fires. The Post binding must still judge it and refuse.
func TestR3_06_RuleQualityJudgesAnUnderivableCreate(t *testing.T) {
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t,
		`{"has_issues": true, "reasoning": "flagged via the Post binding"}`))

	e := New(t)
	proj := project(t, e, "rule-quality")

	e.Run(proj, "s-r3-06", "make a rule the hard way", Turns("done",
		underivableWrite("RULE.md", "# A rule made by a command"),
	))

	// Read from the blocking-error channel, not the mock's stdout. A Post
	// refusal cannot undo the write — the file is on disk and the cycle is
	// over — so it surfaces as a refusal that stops the TURN, which is the
	// mechanism by which an after-the-fact rule gets anything corrected.
	// Asserting on stdout would look for a prevention this timing never
	// performs, and would fail against a perfectly correct engine.
	if !sawRefusal(e.BlockingErrors(proj, "s-r3-06"), "RULE QUALITY") {
		t.Fatalf("an underivable create was never judged — the Post binding did not cover what the Pre kind could not see:\n%v", e.BlockingErrors(proj, "s-r3-06"))
	}
}

// TestR3_07_SkillQualityJudgesAnUnderivableCreate — the same for the sibling.
func TestR3_07_SkillQualityJudgesAnUnderivableCreate(t *testing.T) {
	t.Setenv("A10N_CLAUDE_BIN", stubJudge(t,
		`{"has_issues": true, "reasoning": "flagged via the Post binding"}`))

	e := New(t)
	proj := project(t, e, "skill-quality")

	e.Run(proj, "s-r3-07", "make a skill the hard way", Turns("done",
		underivableWrite("SKILL.md", "# A skill made by a command"),
	))

	if !sawRefusal(e.BlockingErrors(proj, "s-r3-07"), "SKILL QUALITY") {
		t.Fatalf("an underivable create was never judged — the Post binding did not cover what the Pre kind could not see:\n%v", e.BlockingErrors(proj, "s-r3-07"))
	}
}
