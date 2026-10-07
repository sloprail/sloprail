package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// demoCase is a case in the folder of the gate "demo": its owner is that gate.
const demoCase = ".sloprail/gate/demo/tests/c"

const goodTail = `git init -q .
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt x)
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="demo")] | .[0].outcome=="refused" and (.[0].reason|contains("read-only"))' >/dev/null
`

// T042_01: a case with no shebang is refused, saying where the shebang goes.
func TestT042_01_NoShebangRefused(t *testing.T) {
	got := refusalOf(t, demoCase, goodTail)
	if !strings.Contains(got, "does not start with a shebang") || !strings.Contains(got, demoCase) {
		t.Fatalf("a case without a shebang was not refused for it:\n%s", got)
	}
}

// T042_02: a case that asserts only the agent's exit code never asserts the events.
func TestT042_02_NoEventsAssertedRefused(t *testing.T) {
	got := refusalOf(t, demoCase, "#!/usr/bin/env bash\nRESULT=$(sr-test agent a.sh)\necho \"$RESULT\" | jq -e '.exit == 0'\n")
	if !strings.Contains(got, "never asserts on the events") {
		t.Fatalf("a case with no event assertion was not refused for it:\n%s", got)
	}
}

// T042_03: a refusal asserted without its reason is refused.
func TestT042_03_RefusalWithoutReasonRefused(t *testing.T) {
	got := refusalOf(t, demoCase, "#!/usr/bin/env bash\nRESULT=$(sr-test agent a.sh)\necho \"$RESULT\" | jq -e '[.events[]|select(.kind==\"GateChecked\" and .rule==\"demo\")] | .[0].outcome==\"refused\"'\n")
	if !strings.Contains(got, "never checks its reason") {
		t.Fatalf("a refusal asserted without its reason was not refused for it:\n%s", got)
	}
}

// T042_04: swallowing a failure and asserting a constant are each refused.
func TestT042_04_VacuousAssertionsRefused(t *testing.T) {
	got := refusalOf(t, demoCase, "#!/usr/bin/env bash\n"+strings.TrimPrefix(goodTail, "git init -q .\n")+"jq -e '.' <<<'{}'\n[ 1 ]\ntrue\nfalse || true\n")
	for _, want := range []string{"'|| true'", "constant", "only 'true'"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the refusal lacks %q:\n%s", want, got)
		}
	}
}

// T042_05: the case that fixes the one shape passes the floor: the only refusals left are none from the
// script (a judge, unmocked, would run next; the harness error is not a script reason).
func TestT042_05_FixedCaseNotRefusedByTheFloor(t *testing.T) {
	got := refusalOf(t, demoCase, "#!/usr/bin/env bash\nset -euo pipefail\n"+goodTail)
	for _, floor := range []string{"shebang", "never asserts", "never checks its reason", "'|| true'", "constant", "owning rule"} {
		if strings.Contains(got, floor) {
			t.Fatalf("a rigorous-looking case was refused by the script floor for %q:\n%s", floor, got)
		}
	}
}

// T042_06: a case that asserts only the events of another rule is refused, naming its owner (the folder's
// rule) and the event kind the owner's decisions carry.
func TestT042_06_OtherRulesEventsRefused(t *testing.T) {
	got := refusalOf(t, demoCase, "#!/usr/bin/env bash\nRESULT=$(sr-test agent a.sh)\necho \"$RESULT\" | jq -e '[.events[]|select(.kind==\"GateChecked\" and .rule==\"other\")] | .[0].outcome==\"permitted\"'\n")
	if !strings.Contains(got, "asserts no event of its owning rule demo") || !strings.Contains(got, "GateChecked") {
		t.Fatalf("a case asserting another rule's events was not refused for its owner:\n%s", got)
	}
}

// T042_07: the owner's name with the wrong event kind (a file-guard's kind for a gate) is refused as well.
func TestT042_07_WrongEventKindRefused(t *testing.T) {
	got := refusalOf(t, demoCase, "#!/usr/bin/env bash\nRESULT=$(sr-test agent a.sh)\necho \"$RESULT\" | jq -e '[.events[]|select(.kind==\"FileGuardChecked\" and .rule==\"demo\")] | .[0].outcome==\"permitted\"'\n")
	if !strings.Contains(got, "asserts no event of its owning rule demo") {
		t.Fatalf("a case asserting the owner through the wrong event kind was not refused:\n%s", got)
	}
}

// T042_08: a plugin's rule is named <plugin>/<rule>, the plugin being the nearest .claude-plugin/plugin.json
// above the .sloprail: the bare name is refused naming p/demo, and p/demo passes the floor.
func TestT042_08_PluginRuleIsQualified(t *testing.T) {
	e := harness.New(t, harness.WithOnlyShippedFileGuard(ruleName))
	proj := e.Project()
	e.GitInit(proj)
	e.Run(proj, "s-042", "hello", harness.Turns("done"))
	e.WriteFile(proj, "plugins/p/.claude-plugin/plugin.json", `{"name":"p","version":"0.0.1"}`)
	e.WriteFile(proj, "plugins/p/.sloprail/gate/demo/tests/c/test.sh", "#!/usr/bin/env bash\nRESULT=$(sr-test agent a.sh)\necho \"$RESULT\" | jq -e '[.events[]|select(.kind==\"GateChecked\" and .rule==\"demo\")] | .[0].outcome==\"permitted\"'\n")
	e.CommitAll(proj, "add a plugin case")
	got := strings.Join(e.CheckRunRange(proj, "s-042", "origin/main", "HEAD"), "\n")
	if !strings.Contains(got, "asserts no event of its owning rule p/demo") {
		t.Fatalf("the bare name of a plugin's rule was not refused for p/demo:\n%s", got)
	}
	e.WriteFile(proj, "plugins/p/.sloprail/gate/demo/tests/c/test.sh", "#!/usr/bin/env bash\nset -euo pipefail\nRESULT=$(sr-test agent a.sh)\necho \"$RESULT\" | jq -e '[.events[]|select(.kind==\"GateChecked\" and .rule==\"p/demo\")] | .[0].outcome==\"permitted\"'\n")
	e.CommitAll(proj, "assert p/demo")
	got = strings.Join(e.CheckRunRange(proj, "s-042", "origin/main", "HEAD"), "\n")
	if strings.Contains(got, "owning rule") || strings.Contains(got, "shebang") {
		t.Fatalf("the plugin-qualified owner was refused by the floor:\n%s", got)
	}
}

// T042_09: the structure gate's cases live in file-guard/structure.tests/<case>/ and their owner is the
// structure gate: StructureChecked events of the rule "structure".
func TestT042_09_StructureGateCase(t *testing.T) {
	const dir = ".sloprail/file-guard/structure.tests/s"
	got := refusalOf(t, dir, "#!/usr/bin/env bash\nRESULT=$(sr-test agent a.sh)\necho \"$RESULT\" | jq -e '[.events[]|select(.kind==\"GateChecked\" and .rule==\"demo\")] | .[0].outcome==\"permitted\"'\n")
	if !strings.Contains(got, "asserts no event of its owning rule structure") || !strings.Contains(got, "StructureChecked") {
		t.Fatalf("a structure case without StructureChecked events of the structure gate was not refused:\n%s", got)
	}
	got = refusalOf(t, dir, "#!/usr/bin/env bash\nset -euo pipefail\nRESULT=$(sr-test agent a.sh)\necho \"$RESULT\" | jq -e '[.events[]|select(.kind==\"StructureChecked\" and .rule==\"structure\")] | .[0].outcome==\"permitted\" and .[1].outcome==\"refused\" and (.[1].reason|contains(\"deny-by-default\"))'\n")
	if strings.Contains(got, "owning rule") || strings.Contains(got, "shebang") {
		t.Fatalf("a structure case that asserts the structure gate's events was refused by the floor:\n%s", got)
	}
}
