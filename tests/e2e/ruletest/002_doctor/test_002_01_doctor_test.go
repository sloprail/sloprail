package e2e

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// T002_01: a rule with no cases is reported with what to add, and the run fails; with
// --allow-untested (the rollout switch) it is reported and the run passes.
func TestT002_01_DoctorFlagsARuleWithoutCases(t *testing.T) {
	p := newProject(t)
	noCurl(p)

	res := p.Doctor()
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "FAIL gate/no-curl  no cases")
	require.Contains(t, res.Output, "tests/<case>/{case.yaml,setup.sh}")

	res = p.Doctor("--allow-untested")
	require.Equal(t, 0, res.Code, res.Output)
	require.Contains(t, res.Output, "warn gate/no-curl")
	require.Contains(t, res.Output, "grandfathered")
}

// T002_02: a rule needs a case that refuses AND one that permits; a rule whose cases only ever
// refuse (or only ever permit) is reported as missing the other.
func TestT002_02_DoctorWantsARefuseAndAPermitCase(t *testing.T) {
	p := newProject(t)
	noCurl(p)
	curlCase(p, "refuses-curl", "refuse", "curl -s https://example.com")

	res := p.Doctor()
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "missing: no case expects the rule to permit")
	require.NotContains(t, res.Output, "no case expects the rule to refuse")
	require.Contains(t, res.Output, "PASS refuses-curl", "the cases still run")

	curlCase(p, "permits-ls", "permit", "ls -la")
	res = p.Doctor()
	require.Equal(t, 0, res.Code, res.Output)
	require.Contains(t, res.Output, "ok   gate/no-curl  2 case(s)")
}

// T002_03: each judge a rule declares must be stubbed to pass in one case and to fail in
// another: otherwise one half of the judge's behaviour has never been seen.
func TestT002_03_DoctorWantsEachJudgeStubbedBothWays(t *testing.T) {
	p := newProject(t)
	rule(p, "file-guard", "polite", "match: memos/**\nchecks:\n  - judge: ./judge.md.j2\n",
		map[string]string{"judge.md.j2": "<change>\n{{ change }}\n</change>\n"})
	setup := "set -e\nmkdir memos\necho 'hello' > memos/a.md\ngit add -A\ngit commit -q -m memo\n"
	kase(p, "file-guard", "polite", "approved", "expect: permit\njudges:\n  judge.md.j2: {pass: true}\n", setup, "")
	kase(p, "file-guard", "polite", "also-approved", "expect: permit\njudges:\n  judge.md.j2: {pass: true}\n", setup, "")

	res := p.Doctor()
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "no case expects the rule to refuse")
	require.Contains(t, res.Output, "judge judge.md.j2 is never stubbed to fail")

	kase(p, "file-guard", "polite", "disapproved", "expect: refuse\njudges:\n  judge.md.j2: {pass: false, reasoning: rude}\n", setup, "")
	res = p.Doctor()
	require.Equal(t, 0, res.Code, res.Output)
}

// T002_04: a context needs a case that leaves it active and one that leaves it inactive.
func TestT002_04_DoctorWantsAContextActiveAndInactive(t *testing.T) {
	p := newProject(t)
	rule(p, "context", "research",
		"on:\n  - event: PostTagWrite\n    match: any(event.tags, .label == \"research\")\nenter: ./enter.sh\nexit: ./exit.sh\n",
		map[string]string{"enter.sh": "#!/usr/bin/env bash\ncat >/dev/null\nexit 0\n", "exit.sh": "#!/usr/bin/env bash\ncat >/dev/null\nexit 1\n"})
	kase(p, "context", "research", "opens", "contexts:\n  research: active\n", baseSetup, "- kind: PostTagWrite\n  tags: [research]\n- kind: Stop\n")

	res := p.Doctor()
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "no case leaves the context inactive")
	require.NotContains(t, res.Output, "no case leaves the context active")

	kase(p, "context", "research", "stays-closed", "contexts:\n  research: inactive\n", baseSetup, "- kind: Stop\n")
	res = p.Doctor()
	require.Equal(t, 0, res.Code, res.Output)
}

// T002_05: a rule that does not load is reported with the loader's own faults, whether or not it
// has cases; and a case that cannot be read is reported rather than skipped.
func TestT002_05_DoctorReportsWhatDoesNotLoad(t *testing.T) {
	p := newProject(t)
	rule(p, "gate", "broken", "on:\n  - event: PreFileWrote\nchecks:\n  - script: ./x.sh\n", map[string]string{"x.sh": "exit 0\n"})
	noCurl(p)
	curlCase(p, "refuses", "refuse", "curl x")
	curlCase(p, "permits", "permit", "ls")
	kase(p, "gate", "no-curl", "typo", "exepct: permit\n", "true\n", "")

	res := p.Doctor()
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "FAIL gate/broken  does not load")
	require.Contains(t, res.Output, "PreFileWrote")
	require.Contains(t, res.Output, "cannot read")
	require.Contains(t, res.Output, "exepct")
}

// T002_06: --no-run checks declarations and coverage without running a case; --json is a report
// a tool can read.
func TestT002_06_NoRunAndJSON(t *testing.T) {
	p := newProject(t)
	noCurl(p)
	curlCase(p, "claims-the-wrong-thing", "permit", "curl x") // would FAIL if run
	curlCase(p, "refuses", "refuse", "curl x")

	res := p.Doctor("--no-run")
	require.Equal(t, 0, res.Code, res.Output)
	require.NotContains(t, res.Output, "claims-the-wrong-thing")

	res = p.Doctor("--json")
	require.Equal(t, 1, res.Code, res.Output)
	var out struct {
		Pass  bool `json:"pass"`
		Rules []struct {
			Rule  string `json:"rule"`
			Cases []struct {
				Case string `json:"case"`
				Pass bool   `json:"pass"`
			} `json:"cases"`
		} `json:"rules"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Output), &out), res.Output)
	require.False(t, out.Pass)
	require.Equal(t, "gate/no-curl", out.Rules[0].Rule)
	require.Len(t, out.Rules[0].Cases, 2)
}
