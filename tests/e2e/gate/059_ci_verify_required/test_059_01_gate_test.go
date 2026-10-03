package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T059_01: a project with no file-guard has nothing to verify, so no CI marker is asked for.
func TestT059_01_NoFileGuardsNeedsNoMarker(t *testing.T) {
	e, proj := project(t, false)
	if got := stop(e, proj, "s-059-01"); strings.Contains(got, marker) {
		t.Fatalf("a project without file-guards was asked for a CI marker:\n%s", got)
	}
}

// T059_02: file-guards and no committed marker refuses the Stop, and the refusal carries what the
// agent needs: why, a pull-request-only snippet per major provider that verifies the PR head, the marker, how to disable.
func TestT059_02_FileGuardsWithoutMarkerRefuseWithSnippets(t *testing.T) {
	e, proj := project(t, true)
	got := stop(e, proj, "s-059-02")
	for _, want := range []string{
		marker,
		"GitHub Actions", "GitLab CI", "Azure Pipelines", "Jenkins",
		"sr-checks verify", "Protect the default branch",
		"github.event.pull_request.head.sha", "CI_MERGE_REQUEST_DIFF_BASE_SHA", "System.PullRequest.SourceCommitId",
		"sloprail/gate/ci-verify-required",
		"actions/setup-go@v5", "golang:1.25", "GoTool@0",
		"go install github.com/sloprail/sloprail/services/sr-checks@",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the refusal lacks %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"github.event.before", "CI_COMMIT_BEFORE_SHA", "Build.SourceVersion"} {
		if strings.Contains(got, bad) {
			t.Fatalf("the snippets still carry a push-to-default-branch job (%q):\n%s", bad, got)
		}
	}
	if strings.Contains(got, "install.sh | sh") {
		t.Fatalf("the snippets must not install from a release (none carries sr-checks):\n%s", got)
	}
}

// T059_03: the marker committed anywhere (here, a Jenkinsfile comment) satisfies the gate: the
// detection is the marker, not a provider's file path.
func TestT059_03_CommittedMarkerPasses(t *testing.T) {
	e, proj := project(t, true)
	e.WriteFile(proj, "Jenkinsfile", "// "+marker+"\nsh 'sr-checks verify --base origin/main --head $GIT_COMMIT'\n")
	e.CommitAll(proj, "add CI")
	if got := stop(e, proj, "s-059-03"); strings.Contains(got, marker) {
		t.Fatalf("a committed marker did not satisfy the gate:\n%s", got)
	}
}

// T059_04: a marker only in the working tree is a pipeline CI does not run: still refused.
func TestT059_04_UncommittedMarkerStillRefuses(t *testing.T) {
	e, proj := project(t, true)
	e.WriteFile(proj, ".gitlab-ci.yml", "# "+marker+"\n")
	if got := stop(e, proj, "s-059-04"); !strings.Contains(got, marker) {
		t.Fatalf("an uncommitted marker satisfied the gate:\n%s", got)
	}
}

// T059_05: switched off like any shipped rule, by `disabled:` in .sloprail/config.yaml.
func TestT059_05_CanBeDisabledInConfig(t *testing.T) {
	e, proj := project(t, true)
	e.DisablePluginGuardrail(proj, "sloprail/gate/ci-verify-required")
	e.CommitAll(proj, "turn the CI gate off")
	if got := stop(e, proj, "s-059-05"); strings.Contains(got, marker) {
		t.Fatalf("the disabled gate still refused:\n%s", got)
	}
}

// T059_06: the gate applies to the project's OWN file-guards: one a plugin ships (here the
// shipped authoring file-guards, left on) is not the project's to enforce in CI.
func TestT059_06_PluginGuardsAloneNeedNoMarker(t *testing.T) {
	e := harness.New(t, harness.WithCIVerifyRequired())
	e.SetStopBlockCap(1)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project")
	if got := stop(e, proj, "s-059-06"); strings.Contains(got, marker) {
		t.Fatalf("plugin-shipped file-guards alone were asked for a CI marker:\n%s", got)
	}
}

// T059_07: the old free-text form is no marker: the engine's reader does not read it, so a file
// carrying it still leaves the gate refusing.
func TestT059_07_OldTextFormIsRefused(t *testing.T) {
	e, proj := project(t, true)
	e.WriteFile(proj, "Jenkinsfile", "// sr-mark: ci-verify\nsh 'sr-checks verify --base origin/main --head $GIT_COMMIT'\n")
	e.CommitAll(proj, "add CI with the old text")
	if got := stop(e, proj, "s-059-07"); !strings.Contains(got, marker) {
		t.Fatalf("the old text form satisfied the gate:\n%s", got)
	}
}

// T059_08: a marker written by `sr-mark apply ci --verify=<path>:<line>` satisfies the gate.
func TestT059_08_SrMarkApplyWrittenMarkerPasses(t *testing.T) {
	e, proj := project(t, true)
	e.WriteFile(proj, ".gitlab-ci.yml", "job:\n  script:\n    - sr-checks verify --base a --head b\n")
	if res := e.CLIDirectEnv(proj, nil, "sr-mark", "apply", "ci", "--verify=.gitlab-ci.yml:3"); res.Code != 0 {
		t.Fatalf("sr-mark apply failed:\n%s", res.Output)
	}
	e.CommitAll(proj, "add CI")
	if got := stop(e, proj, "s-059-08"); strings.Contains(got, marker) {
		t.Fatalf("a sr-mark-written marker did not satisfy the gate:\n%s", got)
	}
}
