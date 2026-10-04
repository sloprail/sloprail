package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// rule_tests_pass: the plugin's shipped file-guard `sloprail/file-guard/rule-tests-pass`. A change under
// a .sloprail/ runs every sr-test case, and a rule the change adds or edits must be exercised by a case.
// Every other shipped authoring file-guard is off, so what these tests see is this rule alone.

const ruleName = "sloprail/file-guard/rule-tests-pass"

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// refusalOf commits files on top of a clean project and returns what `sr-checks run` refused with.
func refusalOf(t *testing.T, files map[string]string) string {
	t.Helper()
	e := harness.New(t, harness.WithOnlyShippedFileGuard(ruleName))
	proj := e.Project()
	e.GitInit(proj)
	e.Run(proj, "s-043", "hello", harness.Turns("done"))
	for path, body := range files {
		e.WriteExecutable(proj, path, body)
	}
	e.CommitAll(proj, "change the rules")
	return strings.Join(e.CheckRunRange(proj, "s-043", "origin/main", "HEAD"), "\n")
}

const (
	gateYAML = "on:\n  - event: PreFileWrite\n    match: 'event.path startsWith \"notes/\"'\nchecks:\n  - script: ./check.sh\n"
	checkSh  = "#!/usr/bin/env bash\nexit 0\n"
)

type legacyEnv struct {
	t    *testing.T
	e    *harness.Env
	proj string
}

// newEnvWithLegacyRule commits a rule with no case as the base, then a passing case on top of it.
func newEnvWithLegacyRule(t *testing.T) *legacyEnv {
	t.Helper()
	e := harness.New(t, harness.WithOnlyShippedFileGuard(ruleName))
	proj := e.Project()
	e.WriteExecutable(proj, ".sloprail/gate/notes/gate.yaml", gateYAML)
	e.WriteExecutable(proj, ".sloprail/gate/notes/check.sh", checkSh)
	e.GitInit(proj)
	e.Run(proj, "s-043", "hello", harness.Turns("done"))
	e.WriteExecutable(proj, ".sloprail/tests/fine/test.sh", "#!/usr/bin/env bash\nexit 0\n")
	e.CommitAll(proj, "add a case")
	return &legacyEnv{t: t, e: e, proj: proj}
}

func (l *legacyEnv) refusals() []string {
	return l.e.CheckRunRange(l.proj, "s-043", "origin/main", "HEAD")
}
