package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The subject of rule-tests-rigorous is a RULE: every case of the rule is read by the script floor, and the
// refusal names the case that fails it.

// goodCase is a case that passes the script floor for the gate "demo" (it asserts the owner's permit).
const goodCase = "#!/usr/bin/env bash\nset -euo pipefail\nRESULT=$(sr-test agent a.sh)\necho \"$RESULT\" | jq -e '[.events[]|select(.kind==\"GateChecked\" and .rule==\"demo\")] | .[0].outcome==\"permitted\"'\n"

// sloppyCase fails the floor: no shebang, no event asserted.
const sloppyCase = "RESULT=$(sr-test agent a.sh)\necho \"$RESULT\" | jq -e '.exit == 0'\n"

// T042_10: of two cases of one rule, the floor refuses the rule, naming the case that fails it and not the
// one that passes.
func TestT042_10_RuleRefusedNamingTheFailingCase(t *testing.T) {
	e := harness.New(t, harness.WithOnlyShippedFileGuard(ruleName))
	proj := e.Project()
	e.GitInit(proj)
	e.Run(proj, "s-042", "hello", harness.Turns("done"))
	e.WriteFile(proj, demoCase+"/test.sh", goodCase)
	e.WriteFile(proj, ".sloprail/gate/demo/tests/sloppy/test.sh", sloppyCase)
	e.CommitAll(proj, "two cases of one rule")
	got := strings.Join(e.CheckRunRange(proj, "s-042", "origin/main", "HEAD"), "\n")
	if !strings.Contains(got, ".sloprail/gate/demo/tests/sloppy is not a rigorous sr-test case") || !strings.Contains(got, "does not start with a shebang") {
		t.Fatalf("the failing case was not refused by name:\n%s", got)
	}
	if strings.Contains(got, demoCase+" is not") {
		t.Fatalf("the case that passes the floor was named in the refusal:\n%s", got)
	}
}

// T042_11: a change to the RULE alone (a file beside its tests/) selects the rule: its standing cases are
// judged again. The sloppy case is in the base; the range touches only the rule's README.
func TestT042_11_ChangedRuleFileJudgesItsCases(t *testing.T) {
	e := harness.New(t, harness.WithOnlyShippedFileGuard(ruleName))
	proj := e.Project()
	e.GitInit(proj)
	e.Run(proj, "s-042", "hello", harness.Turns("done"))
	e.WriteFile(proj, ".sloprail/gate/demo/tests/sloppy/test.sh", sloppyCase)
	e.WriteFile(proj, ".sloprail/gate/demo/README.md", "demo\n")
	e.CommitAll(proj, "a rule with a sloppy case")
	base := strings.TrimSpace(e.Git(proj, "rev-parse", "HEAD"))
	e.WriteFile(proj, ".sloprail/gate/demo/README.md", "demo, reworded\n")
	e.CommitAll(proj, "reword the rule")
	got := strings.Join(e.CheckRunRange(proj, "s-042", base, "HEAD"), "\n")
	if !strings.Contains(got, ".sloprail/gate/demo/tests/sloppy is not a rigorous sr-test case") {
		t.Fatalf("a changed rule file did not judge its cases:\n%s", got)
	}
}

// T042_12: a rule with no case standing is a subject with nothing to judge: changing it is not refused for
// lacking cases, and neither is a stray file under its tests/ or under structure.tests/ (no case, no refusal).
func TestT042_12_RuleWithoutCasesPasses(t *testing.T) {
	for name, files := range map[string][]string{
		"rule-file":       {".sloprail/gate/demo/README.md"},
		"stray-file":      {".sloprail/gate/demo/tests/stray.txt"},
		"structure-stray": {".sloprail/file-guard/structure.tests/stray.txt"},
		"rule-and-stray":  {".sloprail/gate/demo/README.md", ".sloprail/gate/demo/tests/stray.txt"},
	} {
		t.Run(name, func(t *testing.T) {
			e := harness.New(t, harness.WithOnlyShippedFileGuard(ruleName))
			proj := e.Project()
			e.GitInit(proj)
			e.Run(proj, "s-042", "hello", harness.Turns("done"))
			for _, f := range files {
				e.WriteFile(proj, f, "not a case\n")
			}
			e.CommitAll(proj, "a change with no case")
			if got := strings.Join(e.CheckRunRange(proj, "s-042", "origin/main", "HEAD"), "\n"); got != "" {
				t.Fatalf("a change with no case was refused:\n%s", got)
			}
		})
	}
}
