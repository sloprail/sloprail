package e2e

import (
	"strings"
	"testing"
)

// T041_01: a marked file that runs `sr-checks verify` on pull requests and pushes to the default
// branch passes, on each provider the guard understands.
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

// T041_04: a verify step that never runs on a push to the default branch, or on pull requests, is refused.
func TestT041_04_MissingTriggersRefused(t *testing.T) {
	noPush := strings.Replace(ghValid, "  push:\n    branches: [main]\n", "", 1)
	if got := refusalOf(t, map[string]string{".github/workflows/sloprail.yml": noPush}); !strings.Contains(got, "'push:' trigger") {
		t.Fatalf("a workflow without a push trigger was not refused for it:\n%s", got)
	}
	noPR := strings.Replace(ghValid, "  pull_request:\n", "", 1)
	if got := refusalOf(t, map[string]string{".github/workflows/sloprail.yml": noPR}); !strings.Contains(got, "'pull_request' trigger") {
		t.Fatalf("a workflow without a pull_request trigger was not refused for it:\n%s", got)
	}
	noMR := strings.Replace(glValid, "    - if: $CI_PIPELINE_SOURCE == \"merge_request_event\"\n", "", 1)
	if got := refusalOf(t, map[string]string{".gitlab-ci.yml": noMR}); !strings.Contains(got, "merge_request_event") {
		t.Fatalf("a GitLab file without the merge request rule was not refused for it:\n%s", got)
	}
	noPr := strings.Replace(azValid, "pr: [main]\n", "", 1)
	if got := refusalOf(t, map[string]string{"azure-pipelines.yml": noPr}); !strings.Contains(got, "'pr:'") {
		t.Fatalf("an Azure file without pr: was not refused for it:\n%s", got)
	}
}

// T041_05: a marker in a file of a provider the guard cannot read is refused (unsure fails closed).
func TestT041_05_UnknownProviderRefused(t *testing.T) {
	got := refusalOf(t, map[string]string{"Jenkinsfile": "// sr:ci verify\nsh 'sr-checks verify --base a --head b'\n"})
	if !strings.Contains(got, "not a CI file this guard can check") {
		t.Fatalf("a marker in an unknown provider's file was not refused:\n%s", got)
	}
}
