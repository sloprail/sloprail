package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// `sr-checks test`: the cases a project keeps beside its own rules, run with no model and no
// harness. These tests drive the COMPILED binaries over rules written into a throwaway project,
// and need no mock agent.

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// baseSetup is a case's setup for a trajectory case: one committed file.
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

func sprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }

func newNoTodo(t *testing.T) *harness.RuleProject {
	t.Helper()
	p := harness.NewRuleProject(t)
	noTodoRule(p)
	return p
}

func newPolite(t *testing.T) *harness.RuleProject {
	t.Helper()
	p := harness.NewRuleProject(t)
	politeRule(p)
	return p
}

// refusing is a gate/file-guard check that always refuses with a reason; permitting one that
// never does. `jq` is the one tool they need.
const refusing = "#!/usr/bin/env bash\nset -uo pipefail\ncat >/dev/null\njq -n '{reason: \"%s\"}'\nexit 1\n"

func refusingScript(reason string) string { return strings.Replace(refusing, "%s", reason, 1) }
