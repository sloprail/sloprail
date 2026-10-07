package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// rule_tests_rigorous: the plugin's shipped file-guard `sloprail/file-guard/rule-tests-rigorous`. Its
// script floor refuses an sr-test case that would pass whatever its rule does. Every other shipped
// authoring file-guard is off, so what these tests see is this rule alone. (The judge half is proved by
// the sr-test case rule-tests-rigorous-judge-decides, with a mocked judge.)

const ruleName = "sloprail/file-guard/rule-tests-rigorous"

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// refusalOf commits a case's test.sh on top of a clean project and returns what `sr-checks run` refused
// with ("" when the change passes the script floor and no judge was asked: a refusal is all these assert).
func refusalOf(t *testing.T, caseDir, testSh string) string {
	t.Helper()
	e := harness.New(t, harness.WithOnlyShippedFileGuard(ruleName))
	proj := e.Project()
	e.GitInit(proj)
	e.Run(proj, "s-042", "hello", harness.Turns("done"))
	e.WriteFile(proj, caseDir+"/test.sh", testSh)
	e.CommitAll(proj, "add a case")
	return strings.Join(e.CheckRunRange(proj, "s-042", "origin/main", "HEAD"), "\n")
}
