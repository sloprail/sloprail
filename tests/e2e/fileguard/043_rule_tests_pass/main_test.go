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
	passCase = "#!/usr/bin/env bash\nexit 0\n"
	failCase = "#!/usr/bin/env bash\necho 'the broken one' >&2\nexit 1\n"
)

// notes returns the files of the gate "notes": its declaration and script, plus the cases given (name -> test.sh),
// each in the gate's own folder.
func notes(cases map[string]string) map[string]string {
	files := map[string]string{
		".sloprail/gate/notes/gate.yaml": gateYAML,
		".sloprail/gate/notes/check.sh":  checkSh,
	}
	for name, body := range cases {
		files[".sloprail/gate/notes/tests/"+name+"/test.sh"] = body
	}
	return files
}

// scopeEnv commits the gate "notes" with a passing case "good" and a broken one "broken" as the base, and a
// legacy gate "old" with no case at all. Nothing in the range yet.
func scopeEnv(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := harness.New(t, harness.WithOnlyShippedFileGuard(ruleName))
	proj := e.Project()
	for path, body := range notes(map[string]string{"good": passCase, "broken": failCase}) {
		e.WriteExecutable(proj, path, body)
	}
	e.WriteExecutable(proj, ".sloprail/gate/old/gate.yaml", gateYAML)
	e.WriteExecutable(proj, ".sloprail/gate/old/check.sh", checkSh)
	e.GitInit(proj)
	e.Run(proj, "s-043", "hello", harness.Turns("done"))
	return e, proj
}
