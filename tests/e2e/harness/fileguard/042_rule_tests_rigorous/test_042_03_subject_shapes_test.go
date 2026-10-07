package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The subject's shape beyond a gate in the root project: the structure gate (its rule is the one file
// structure.yaml, its cases are structure.tests/<case>/) and a plugin's nested .sloprail.

const (
	structureYAML  = "allow:\n  - glob: \"**\"\n"
	structureGood  = "#!/usr/bin/env bash\nset -euo pipefail\nRESULT=$(sr-test agent a.sh)\necho \"$RESULT\" | jq -e '[.events[]|select(.kind==\"StructureChecked\" and .rule==\"structure\")] | .[0].outcome==\"permitted\"'\n"
	pluginGoodCase = "#!/usr/bin/env bash\nset -euo pipefail\nRESULT=$(sr-test agent a.sh)\necho \"$RESULT\" | jq -e '[.events[]|select(.kind==\"GateChecked\" and .rule==\"p/demo\")] | .[0].outcome==\"permitted\"'\n"
)

func floorSession(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := harness.New(t, harness.WithOnlyShippedFileGuard(ruleName))
	proj := e.Project()
	e.GitInit(proj)
	e.Run(proj, "s-042", "hello", harness.Turns("done"))
	return e, proj
}

// T042_13: a change to the structure gate's rule file, alone, judges its standing cases: the sloppy one is
// refused by name, the good one is not.
func TestT042_13_StructureFileJudgesItsCases(t *testing.T) {
	e, proj := floorSession(t)
	e.WriteFile(proj, ".sloprail/file-guard/structure.yaml", structureYAML)
	e.WriteFile(proj, ".sloprail/file-guard/structure.tests/good/test.sh", structureGood)
	e.WriteFile(proj, ".sloprail/file-guard/structure.tests/sloppy/test.sh", sloppyCase)
	e.CommitAll(proj, "the structure gate and two cases")
	base := strings.TrimSpace(e.Git(proj, "rev-parse", "HEAD"))
	e.WriteFile(proj, ".sloprail/file-guard/structure.yaml", "allow:\n  - glob: \"**\"\n  - glob: \"docs/**\"\n")
	e.CommitAll(proj, "widen the structure gate")
	got := strings.Join(e.CheckRunRange(proj, "s-042", base, "HEAD"), "\n")
	if !strings.Contains(got, ".sloprail/file-guard/structure.tests/sloppy is not a rigorous sr-test case") {
		t.Fatalf("a changed structure.yaml did not judge its cases:\n%s", got)
	}
	if strings.Contains(got, "structure.tests/good is not") {
		t.Fatalf("the structure case that passes the floor was named:\n%s", got)
	}
}

// T042_16: a stray file under a rule's tests/ (or under structure.tests/) is no case but still selects its
// rule: the sloppy case standing in the base is judged and named.
func TestT042_16_StrayFileSelectsItsRule(t *testing.T) {
	for name, c := range map[string]struct{ stray, sloppy, want string }{
		"rule": {".sloprail/gate/demo/tests/stray.txt", ".sloprail/gate/demo/tests/sloppy/test.sh", ".sloprail/gate/demo/tests/sloppy is not a rigorous"},
		"structure": {".sloprail/file-guard/structure.tests/stray.txt", ".sloprail/file-guard/structure.tests/sloppy/test.sh",
			".sloprail/file-guard/structure.tests/sloppy is not a rigorous"},
	} {
		t.Run(name, func(t *testing.T) {
			e, proj := floorSession(t)
			e.WriteFile(proj, c.sloppy, sloppyCase)
			e.CommitAll(proj, "a sloppy case")
			base := strings.TrimSpace(e.Git(proj, "rev-parse", "HEAD"))
			e.WriteFile(proj, c.stray, "not a case\n")
			e.CommitAll(proj, "a stray file")
			got := strings.Join(e.CheckRunRange(proj, "s-042", base, "HEAD"), "\n")
			if !strings.Contains(got, c.want) {
				t.Fatalf("a stray file did not select its rule:\n%s", got)
			}
		})
	}
}

// T042_15: a changed file whose name holds a tab still selects its rule whole (paths are NUL-delimited): the
// case folder it sits in has no test.sh, and the refusal says so naming that case.
func TestT042_15_TabInAPathStillSelectsTheRule(t *testing.T) {
	e, proj := floorSession(t)
	e.WriteFile(proj, ".sloprail/gate/demo/tests/odd/a\tb.txt", "x\n")
	e.CommitAll(proj, "a file with a tab in its name")
	got := strings.Join(e.CheckRunRange(proj, "s-042", "origin/main", "HEAD"), "\n")
	if !strings.Contains(got, ".sloprail/gate/demo/tests/odd has no test.sh") {
		t.Fatalf("a path with a tab did not select its rule:\n%s", got)
	}
}

// T042_14: a plugin's nested .sloprail is its own subject, named by its folder under the root, with the rule
// qualified <plugin>/<rule>; the changed rule file selects it and only the failing case is named.
func TestT042_14_NestedPluginRootIsOneSubject(t *testing.T) {
	e, proj := floorSession(t)
	const root = "plugins/p/.sloprail/gate/demo"
	e.WriteFile(proj, "plugins/p/.claude-plugin/plugin.json", `{"name":"p","version":"0.0.1"}`)
	e.WriteFile(proj, root+"/README.md", "demo\n")
	e.WriteFile(proj, root+"/tests/ok/test.sh", pluginGoodCase)
	e.WriteFile(proj, root+"/tests/bad/test.sh", sloppyCase)
	e.CommitAll(proj, "a plugin rule with two cases")
	got := strings.Join(e.CheckRunRange(proj, "s-042", "origin/main", "HEAD"), "\n")
	if !strings.Contains(got, "the sr-test cases of "+root+" are not rigorous") ||
		!strings.Contains(got, root+"/tests/bad is not a rigorous sr-test case") {
		t.Fatalf("the plugin rule's failing case was not refused by name:\n%s", got)
	}
	if strings.Contains(got, root+"/tests/ok is not") {
		t.Fatalf("the plugin case that passes the floor was named:\n%s", got)
	}
}
