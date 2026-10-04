package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// `sr-checks doctor`: every project rule loads, is proved by cases, and the cases pass. Driven
// through the COMPILED binaries over rules written into a throwaway project; no mock agent.

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

func newProject(t *testing.T) *harness.RuleProject { return harness.NewRuleProject(t) }

// baseSetup is a case's setup: one committed file.
const baseSetup = "echo hello > README.md\ngit add -A\ngit commit -q -m base\n"

// rule writes a rule's declaration and scripts under the project's .sloprail.
func rule(p *harness.RuleProject, nature, name, yaml string, files map[string]string) {
	p.Write(".sloprail/"+nature+"/"+name+"/"+nature+".yaml", yaml)
	for f, body := range files {
		p.Write(".sloprail/"+nature+"/"+name+"/"+f, body)
	}
}

// kase writes a case beside a rule.
func kase(p *harness.RuleProject, nature, name, caseName, caseYAML, setup, trajectory string) {
	dir := ".sloprail/" + nature + "/" + name + "/tests/" + caseName + "/"
	p.Write(dir+"case.yaml", caseYAML)
	p.Write(dir+"setup.sh", setup)
	if trajectory != "" {
		p.Write(dir+"trajectory.yaml", trajectory)
	}
}

const refusing = "#!/usr/bin/env bash\nset -uo pipefail\ncat >/dev/null\njq -n '{reason: \"%s\"}'\nexit 1\n"

func refusingScript(reason string) string { return strings.Replace(refusing, "%s", reason, 1) }

// noCurl is a gate refusing a curl command line, with the rule's own scripts.
func noCurl(p *harness.RuleProject) {
	rule(p, "gate", "no-curl",
		"on:\n  - event: PreCommandInvoke\n    match: any(event.invocations, .bin == \"curl\")\nchecks:\n  - script: ./no.sh\n",
		map[string]string{"no.sh": refusingScript("use the fetch tool")})
}

func curlCase(p *harness.RuleProject, name, expect, command string) {
	kase(p, "gate", "no-curl", name, "description: x\n", baseSetup,
		"- kind: PreCommandInvoke\n  command: '"+command+"'\n  expect: "+expect+"\n")
}
