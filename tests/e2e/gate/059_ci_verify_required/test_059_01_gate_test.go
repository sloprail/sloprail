package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
// agent needs: why, a snippet per major provider that verifies the PR head, the marker, how to disable.
func TestT059_02_FileGuardsWithoutMarkerRefuseWithSnippets(t *testing.T) {
	e, proj := project(t, true)
	got := stop(e, proj, "s-059-02")
	for _, want := range []string{
		marker,
		"GitHub Actions", "GitLab CI", "Azure Pipelines", "Jenkins",
		"sr-checks verify", "push to the default branch", "github.event.before", "CI_COMMIT_BEFORE_SHA", "Build.SourceVersion", "merge-base",
		"github.event.pull_request.head.sha", "CI_MERGE_REQUEST_DIFF_BASE_SHA", "System.PullRequest.SourceCommitId",
		"sloprail/gate/ci-verify-required",
		"/install.sh | SLOPRAIL_INSTALL_TAG=",
		`>> "$GITHUB_PATH"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the refusal lacks %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"actions/setup-go", "golang:1.25", "GoTool@0", "go install", "no sloprail release tarball"} {
		if strings.Contains(got, bad) {
			t.Fatalf("the snippets must install from the release, not with Go (%q):\n%s", bad, got)
		}
	}
	raw, err := os.ReadFile(filepath.Join(srcRoot(), "marketplace/plugins/sloprail/.claude-plugin/plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pj struct{ Version string }
	if err := json.Unmarshal(raw, &pj); err != nil || pj.Version == "" {
		t.Fatalf("plugin.json version unreadable: %v", err)
	}
	tag := "v" + pj.Version
	if n := strings.Count(got, "/sloprail/"+tag+"/install.sh | SLOPRAIL_INSTALL_TAG="+tag+" sh"); n != 3 {
		t.Fatalf("want 3 install lines pinned to the plugin version %s, got %d:\n%s", tag, n, got)
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

// T059_07: the refusal is a one-time notice: the agent cannot fix it alone (CI and config changes
// need the user), so a second Stop in the same session is let through.
func TestT059_07_RefusesOncePerSession(t *testing.T) {
	e, proj := project(t, true)
	if got := stop(e, proj, "s-059-07"); !strings.Contains(got, "ONE-TIME NOTICE") || !strings.Contains(got, marker) {
		t.Fatalf("the first Stop was not refused with the notice:\n%s", got)
	}
	if got := stop(e, proj, "s-059-07"); strings.Contains(got, marker) {
		t.Fatalf("the second Stop in the same session was refused again:\n%s", got)
	}
	if got := stop(e, proj, "s-059-07"); strings.Contains(got, marker) {
		t.Fatalf("the third Stop in the same session was refused again:\n%s", got)
	}
}

// T059_08: a new session is refused once again.
func TestT059_08_NewSessionIsRefusedAgain(t *testing.T) {
	e, proj := project(t, true)
	stop(e, proj, "s-059-08a")
	if got := stop(e, proj, "s-059-08b"); !strings.Contains(got, marker) {
		t.Fatalf("a new session was not refused:\n%s", got)
	}
}

// T059_09: a state change re-arms the notice: a project that had no file-guards (nothing said) and
// then adds one is refused once, and the marker found then removed is refused once again.
func TestT059_09_StateChangeRefusesAgain(t *testing.T) {
	e, proj := project(t, false)
	sess := "s-059-09"
	if got := stop(e, proj, sess); strings.Contains(got, marker) {
		t.Fatalf("a project without file-guards was refused:\n%s", got)
	}
	e.FileGuard(proj, "docs", "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n", map[string]string{"check.sh": passCheck})
	e.CommitAll(proj, "the rule")
	if got := stop(e, proj, sess); !strings.Contains(got, marker) {
		t.Fatalf("a newly added file-guard was not refused:\n%s", got)
	}
	if got := stop(e, proj, sess); strings.Contains(got, marker) {
		t.Fatalf("the same state was refused twice:\n%s", got)
	}
	e.WriteFile(proj, "Jenkinsfile", "// "+marker+"\n")
	e.CommitAll(proj, "add CI")
	if got := stop(e, proj, sess); strings.Contains(got, marker) {
		t.Fatalf("a committed marker was refused:\n%s", got)
	}
	e.WriteFile(proj, "Jenkinsfile", "nothing\n")
	e.CommitAll(proj, "drop CI")
	if got := stop(e, proj, sess); !strings.Contains(got, marker) {
		t.Fatalf("a removed marker was not refused again:\n%s", got)
	}
}

// T059_10: a repository with the marker always passes, Stop after Stop.
func TestT059_10_MarkerAlwaysPasses(t *testing.T) {
	e, proj := project(t, true)
	e.WriteFile(proj, "Jenkinsfile", "// "+marker+"\n")
	e.CommitAll(proj, "add CI")
	for i := 0; i < 3; i++ {
		if got := stop(e, proj, "s-059-10"); strings.Contains(got, marker) {
			t.Fatalf("Stop %d with the marker was refused:\n%s", i, got)
		}
	}
}

// srcRoot is the repository root, found from this test file own path.
func srcRoot() string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..", "..", "..")
}
