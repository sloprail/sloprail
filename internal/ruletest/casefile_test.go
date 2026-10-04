package ruletest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
)

// writeCase lays a case folder down under dir/tests/<name>.
func writeCase(t *testing.T, dir, name string, files map[string]string) {
	t.Helper()
	for f, body := range files {
		p := filepath.Join(dir, "tests", name, f)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
}

func TestLoadCases_ReadsACase(t *testing.T) {
	dir := t.TempDir()
	writeCase(t, dir, "refuses", map[string]string{
		"case.yaml":       "expect: refuse\nreason_contains: [a, b]\njudges:\n  judge.md.j2: {pass: false, reasoning: nope}\n",
		"setup.sh":        "true\n",
		"trajectory.yaml": "- kind: Stop\n",
	})
	cases, errs := LoadCases(dir)
	require.Empty(t, errs)
	require.Len(t, cases, 1)
	c := cases[0]
	require.Equal(t, "refuses", c.Name)
	require.Equal(t, ExpectRefuse, c.Expect)
	require.Equal(t, StringList{"a", "b"}, c.ReasonContains)
	require.False(t, c.Judges["judge.md.j2"].Pass)
	require.Len(t, c.Trajectory, 1)
}

func TestLoadCases_NoTestsDirIsNoCases(t *testing.T) {
	cases, errs := LoadCases(t.TempDir())
	require.Empty(t, cases)
	require.Empty(t, errs)
}

func TestLoadCases_RefusesWhatAssertsLessThanItsAuthorThinks(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		want  string
	}{
		"a mistyped key": {
			map[string]string{"case.yaml": "exepct: refuse\n", "setup.sh": "true\n"},
			"exepct",
		},
		"expects nothing": {
			map[string]string{"case.yaml": "description: x\n", "setup.sh": "true\n"},
			"expects nothing",
		},
		"a bad verdict": {
			map[string]string{"case.yaml": "expect: maybe\n", "setup.sh": "true\n"},
			"refuse or permit",
		},
		"no setup": {
			map[string]string{"case.yaml": "expect: permit\n"},
			"no setup.sh",
		},
		"a failing stub with no reasoning": {
			map[string]string{"case.yaml": "expect: refuse\njudges:\n  j.md.j2: {pass: false}\n", "setup.sh": "true\n"},
			"no reasoning",
		},
		"contexts without a trajectory": {
			map[string]string{"case.yaml": "contexts: {x: active}\n", "setup.sh": "true\n"},
			"none",
		},
		"an unknown context state": {
			map[string]string{"case.yaml": "contexts: {x: awake}\n", "setup.sh": "true\n", "trajectory.yaml": "- kind: Stop\n"},
			"active or inactive",
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeCase(t, dir, "c", tc.files)
			cases, errs := LoadCases(dir)
			require.Empty(t, cases)
			require.Len(t, errs, 1)
			require.Contains(t, errs[0].Error(), tc.want)
		})
	}
}

func TestParseTrajectory_Steps(t *testing.T) {
	steps, err := parseTrajectory([]byte(`
- kind: PreFileCreate
  path: a.md
  newContent: "x"
  agent: scout
  expect: refuse
  reason_contains: why
- run: |
    echo hi
- subagent: start
  id: scout
- subagent: stop
  id: scout
  expect: permit
- checks_run: {base: main}
- contexts: {research: active}
`))
	require.NoError(t, err)
	require.Len(t, steps, 6)
	require.Equal(t, StepEvent, steps[0].Type)
	require.Equal(t, "scout", steps[0].Agent)
	require.Equal(t, map[string]any{"path": "a.md", "newContent": "x"}, steps[0].Event)
	require.Equal(t, []string{"why"}, steps[0].ReasonContains)
	require.Equal(t, StepRun, steps[1].Type)
	require.Equal(t, StepSubagent, steps[2].Type)
	require.Equal(t, "stop", steps[3].Action)
	require.Equal(t, "main", steps[4].ChecksBase)
	require.Equal(t, StepAssert, steps[5].Type)
}

func TestParseTrajectory_RejectsMalformedSteps(t *testing.T) {
	for name, body := range map[string]string{
		"not a list":             "kind: Stop\n",
		"empty":                  "[]\n",
		"two things in one":      "- kind: Stop\n  run: ls\n",
		"nothing to do":          "- path: a\n",
		"a bad expect":           "- kind: Stop\n  expect: maybe\n",
		"expect on run":          "- run: ls\n  expect: permit\n",
		"agent on run":           "- run: ls\n  agent: a\n",
		"a refusable start":      "- subagent: start\n  id: a\n  expect: refuse\n",
		"subagent without id":    "- subagent: stop\n",
		"subagent bad action":    "- subagent: pause\n  id: a\n",
		"a bad context state":    "- contexts: {x: maybe}\n",
		"checks_run unknown key": "- checks_run: {head: x}\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseTrajectory([]byte(body))
			require.Error(t, err)
		})
	}
}

func rule(n declaration.Nature, name string, judges ...string) Rule {
	return Rule{Nature: n, Name: name, Judges: judges}
}

func TestCoverage_NeedsARefuseAndAPermitCase(t *testing.T) {
	r := rule(declaration.NatureGate, "g")
	require.Len(t, Coverage(r, nil), 2)
	require.Len(t, Coverage(r, []Case{{Expect: ExpectRefuse}}), 1)
	require.Empty(t, Coverage(r, []Case{{Expect: ExpectRefuse}, {Expect: ExpectPermit}}))
	// a trajectory's per-step expectations count
	steps := []Step{{Type: StepEvent, Expect: ExpectRefuse}, {Type: StepEvent, Expect: ExpectPermit}}
	require.Empty(t, Coverage(r, []Case{{Trajectory: steps}}))
}

func TestCoverage_EachJudgeStubbedBothWays(t *testing.T) {
	r := rule(declaration.NatureFileGuard, "g", "judge.md.j2")
	pass := Case{Expect: ExpectPermit, Judges: map[string]JudgeStub{"judge.md.j2": {Pass: true}}}
	fail := Case{Expect: ExpectRefuse, Judges: map[string]JudgeStub{"./judge.md.j2": {Pass: false, Reasoning: "no"}}}

	got := Coverage(r, []Case{pass, {Expect: ExpectRefuse}})
	require.Len(t, got, 1)
	require.Contains(t, got[0], "never stubbed to fail")

	got = Coverage(r, []Case{fail, {Expect: ExpectPermit}})
	require.Len(t, got, 1)
	require.Contains(t, got[0], "never stubbed to pass")

	require.Empty(t, Coverage(r, []Case{pass, fail}))
}

func TestCoverage_AContextIsActiveInOneCaseAndInactiveInAnother(t *testing.T) {
	r := rule(declaration.NatureContext, "research")
	require.Len(t, Coverage(r, nil), 2)
	on := Case{Contexts: map[string]string{"research": "active"}}
	off := Case{Trajectory: []Step{{Type: StepAssert, Contexts: map[string]string{"research": "inactive"}}}}
	require.Empty(t, Coverage(r, []Case{on, off}))
	require.Len(t, Coverage(r, []Case{on}), 1)
}

func TestRuleSelectors(t *testing.T) {
	rules := []Rule{
		{Nature: declaration.NatureGate, Name: "a"},
		{Nature: declaration.NatureContext, Name: "a"},
		{Nature: declaration.NatureGate, Name: "b", Plugin: "sloprail"},
	}
	got, err := Select(rules, []string{"gate/a"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	got, err = Select(rules, []string{"a"})
	require.NoError(t, err)
	require.Len(t, got, 2, "a bare name selects every nature that has it")
	got, err = Select(rules, []string{"sloprail/gate/b"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	_, err = Select(rules, []string{"nope"})
	require.ErrorContains(t, err, "no rule matches")
	got, err = Select(rules, nil)
	require.NoError(t, err)
	require.Len(t, got, 3)
}

func TestExpandJudgeKeys(t *testing.T) {
	r := rule(declaration.NatureFileGuard, "memo")
	got := ExpandJudgeKeys(r, map[string]JudgeStub{
		"judge.md.j2":                 {Pass: true},
		"gate/other/sub/judge2.md.j2": {Pass: false, Reasoning: "x"},
	})
	require.Contains(t, got, "file-guard/memo/judge.md.j2")
	require.Contains(t, got, "gate/other/sub/judge2.md.j2")
}
