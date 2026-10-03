package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// ci_verify_step: the plugin's shipped file-guard `sloprail/file-guard/ci-verify-step`. A committed
// file carrying the marker `sr:ci verify` must run `sr-checks verify` and trigger on pull requests
// (a push trigger is allowed, never required: the default branch is protected). Judged by `sr-checks run` over a range, like any file-guard.
// Every other shipped authoring file-guard is off, so what these tests see is this rule alone.

const ruleName = "sloprail/file-guard/ci-verify-step"

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// refusalOf commits files on top of a clean project and returns what `sr-checks run` refused
// with ("" when the change passes).
func refusalOf(t *testing.T, files map[string]string) string {
	t.Helper()
	e := harness.New(t, harness.WithOnlyShippedFileGuard(ruleName))
	proj := e.Project()
	e.GitInit(proj)
	e.Run(proj, "s-041", "hello", harness.Turns("done"))
	for path, body := range files {
		e.WriteFile(proj, path, body)
	}
	e.CommitAll(proj, "add CI")
	return strings.Join(e.CheckRunRange(proj, "s-041", "origin/main", "HEAD"), "\n")
}

const (
	ghValid = `name: sloprail
on:
  pull_request:
jobs:
  v:
    runs-on: ubuntu-latest
    steps:
      # sr:ci verify
      - run: sr-checks verify --base origin/main --head "$GITHUB_SHA"
`
	glValid = `sloprail-verify:
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
  script:
    # sr:ci verify
    - sr-checks verify --base "$BASE" --head "$CI_COMMIT_SHA"
`
	azValid = `pr: [main]
steps:
  # sr:ci verify
  - script: sr-checks verify --base origin/main --head $(Build.SourceVersion)
`
)
