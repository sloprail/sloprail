package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

const politeJudge = "## Is this memo polite?\n\n<change>\n{{ change }}\n</change>\n"

func politeRule(p *harness.RuleProject) {
	rule(p, "file-guard", "polite", "match: memos/**\nchecks:\n  - judge: ./judge.md.j2\n", map[string]string{"judge.md.j2": politeJudge})
}

const memoSetup = "set -e\nmkdir memos\nprintf '%s\\n' 'Dear all, thank you.' > memos/a.md\ngit add -A\ngit commit -q -m memo\n"

// T001_10: a judge is answered from the case's canned verdict, both ways, and the model is
// never called: the stub's reasoning is the refusal, and prompt_contains proves what the
// judge would have been shown.
func TestT001_10_AJudgeIsStubbedBothWays(t *testing.T) {
	p := harness.NewRuleProject(t)
	politeRule(p)
	kase(p, "file-guard", "polite", "judge-approves",
		"expect: permit\njudges:\n  judge.md.j2:\n    pass: true\n    prompt_contains: [\"Dear all, thank you.\"]\n", memoSetup, "")
	kase(p, "file-guard", "polite", "judge-disapproves",
		"expect: refuse\nreason_contains: too curt\njudges:\n  judge.md.j2:\n    pass: false\n    reasoning: the memo is too curt\n", memoSetup, "")

	res := p.Test()
	require.Equal(t, 0, res.Code, res.Output)
	require.Contains(t, res.Output, "PASS judge-approves")
	require.Contains(t, res.Output, "PASS judge-disapproves")
	require.Contains(t, res.Output, "the memo is too curt")
}

// T001_11: a case that reaches a judge it does not stub FAILS: it never calls a model.
func TestT001_11_AnUnstubbedJudgeFailsTheCase(t *testing.T) {
	p := harness.NewRuleProject(t)
	politeRule(p)
	kase(p, "file-guard", "polite", "forgot-the-stub", "expect: permit\n", memoSetup, "")

	res := p.Test()
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "FAIL forgot-the-stub")
	require.Contains(t, res.Output, "which this case does not stub")
}

// T001_12: a stub the rule never reaches fails the case (the rule stopped asking the judge),
// unless the stub says it is optional; and a prompt that lacks what the case required fails too.
func TestT001_12_AStubNeverAskedAndAPromptThatLacksWhatWasRequired(t *testing.T) {
	p := harness.NewRuleProject(t)
	rule(p, "file-guard", "cheap-first", "match: memos/**\nchecks:\n  - script: ./no.sh\n  - judge: ./judge.md.j2\n",
		map[string]string{"no.sh": refusingScript("memos are closed"), "judge.md.j2": politeJudge})
	kase(p, "file-guard", "cheap-first", "refuses-before-the-judge",
		"expect: refuse\nreason_contains: memos are closed\njudges:\n  judge.md.j2:\n    pass: true\n", memoSetup, "")
	kase(p, "file-guard", "cheap-first", "optional-stub-is-fine",
		"expect: refuse\njudges:\n  judge.md.j2:\n    pass: true\n    optional: true\n", memoSetup, "")

	politeRule(p)
	kase(p, "file-guard", "polite", "prompt-lacks-it",
		"expect: permit\njudges:\n  judge.md.j2:\n    pass: true\n    prompt_contains: [\"a sentence the memo does not hold\"]\n", memoSetup, "")

	res := p.Test("cheap-first")
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "FAIL refuses-before-the-judge")
	require.Contains(t, res.Output, "is stubbed but was never asked")
	require.Contains(t, res.Output, "PASS optional-stub-is-fine")

	res = p.Test("polite")
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "does not contain")
}

// T001_13: the judge's template is rendered for real, so one that does not render fails the
// case exactly as it would live.
func TestT001_13_ABrokenJudgeTemplateFailsTheCase(t *testing.T) {
	p := harness.NewRuleProject(t)
	rule(p, "file-guard", "polite", "match: memos/**\nchecks:\n  - judge: ./judge.md.j2\n",
		map[string]string{"judge.md.j2": "{{ change | uppper }}\n"})
	kase(p, "file-guard", "polite", "renders", "expect: permit\njudges:\n  judge.md.j2:\n    pass: true\n", memoSetup, "")

	res := p.Test()
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "FAIL renders")
	require.Contains(t, res.Output, "uppper")
}
