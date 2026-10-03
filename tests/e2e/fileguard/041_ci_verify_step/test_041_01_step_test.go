package e2e

import (
	"strings"
	"testing"
)

// T041_01: a marked file that runs `sr-checks verify` on pull requests
// passes, on each provider the guard understands.
func TestT041_01_ValidMarkedFilePasses(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"github": {".github/workflows/sloprail.yml": ghValid},
		"gitlab": {".gitlab-ci.yml": glValid},
		"azure":  {"azure-pipelines.yml": azValid},
	} {
		t.Run(name, func(t *testing.T) {
			if got := refusalOf(t, files); got != "" {
				t.Fatalf("a valid %s CI file was refused:\n%s", name, got)
			}
		})
	}
}

// T041_02: the old free-text form is not a marker: the file-guard does not select the file (the
// gate sloprail/gate/ci-verify-required refuses it, see 059).
func TestT041_02_OldTextFormIsNotAMarker(t *testing.T) {
	old := strings.ReplaceAll(ghValid, "# sr:ci verify", "# sr-mark: ci-verify")
	if got := refusalOf(t, map[string]string{".github/workflows/sloprail.yml": old}); got != "" {
		t.Fatalf("the old text form was treated as a marker:\n%s", got)
	}
}

// T041_03: a marker with no `sr-checks verify` step is refused, naming the step.
func TestT041_03_MarkerWithoutVerifyStepRefused(t *testing.T) {
	noStep := strings.ReplaceAll(ghValid, "sr-checks verify", "echo hello")
	got := refusalOf(t, map[string]string{".github/workflows/sloprail.yml": noStep})
	for _, want := range []string{"ci-verify-step", ".github/workflows/sloprail.yml", "sr-checks verify"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the refusal lacks %q:\n%s", want, got)
		}
	}
}

// T041_04: a verify step with no pull request trigger is refused; a push trigger alone does not do.
func TestT041_04_MissingPullRequestTriggerRefused(t *testing.T) {
	onlyPush := strings.Replace(ghValid, "  pull_request:\n", "  push:\n    branches: [main]\n", 1)
	if got := refusalOf(t, map[string]string{".github/workflows/sloprail.yml": onlyPush}); !strings.Contains(got, "'pull_request' trigger") {
		t.Fatalf("a workflow with only a push trigger was not refused for the missing pull_request:\n%s", got)
	}
	noMR := strings.Replace(glValid, "    - if: $CI_PIPELINE_SOURCE == \"merge_request_event\"\n", "    - if: $CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH\n", 1)
	if got := refusalOf(t, map[string]string{".gitlab-ci.yml": noMR}); !strings.Contains(got, "merge_request_event") {
		t.Fatalf("a GitLab file without the merge request rule was not refused for it:\n%s", got)
	}
	noPr := strings.Replace(azValid, "pr: [main]\n", "trigger: [main]\n", 1)
	if got := refusalOf(t, map[string]string{"azure-pipelines.yml": noPr}); !strings.Contains(got, "'pr:'") {
		t.Fatalf("an Azure file without pr: was not refused for it:\n%s", got)
	}
}

// T041_06: a push trigger beside the pull request one is allowed.
func TestT041_06_PushTriggerIsAllowed(t *testing.T) {
	both := strings.Replace(ghValid, "  pull_request:\n", "  pull_request:\n  push:\n    branches: [main]\n", 1)
	if got := refusalOf(t, map[string]string{".github/workflows/sloprail.yml": both}); got != "" {
		t.Fatalf("a push trigger beside pull_request was refused:\n%s", got)
	}
}

// T041_05: any other provider's file passes with the marker and an `sr-checks verify` line, and is
// refused when no line runs it.
func TestT041_05_OtherProviderNeedsOnlyTheVerifyLine(t *testing.T) {
	if got := refusalOf(t, map[string]string{"Jenkinsfile": "// sr:ci verify\nsh 'sr-checks verify --base a --head b'\n"}); got != "" {
		t.Fatalf("a Jenkinsfile with the marker and a verify line was refused:\n%s", got)
	}
	got := refusalOf(t, map[string]string{"Jenkinsfile": "// sr:ci verify\nsh 'make test'\n"})
	if !strings.Contains(got, "sr-checks verify") {
		t.Fatalf("a Jenkinsfile with the marker and no verify line was not refused:\n%s", got)
	}
}
