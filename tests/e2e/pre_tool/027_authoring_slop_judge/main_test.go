// authoring-slop's JUDGE check — the reasoning half of the guardrail that
// validates sloprail's OWN guardrail scripts and prompt templates. The grep
// (check-rules.sh) catches exact signatures; the judge catches what a signature
// cannot decide. This package proves the headline case: a script that reads
// `newContent` on `PreFileCreate` in a branch that ASSUMES the create is
// derivable — the exact 3x bug — is PERMITTED by the grep but REFUSED by the
// judge, and a correct kind-dispatch script passes both.
//
// Driven through the claude-MOCK like the rest of the e2e: the harness runs the
// mock, whose Write tool calls fire this repo's real plugin, reaching the real
// authoring-slop files. Only the MODEL the judge itself invokes is replaced — by
// InstallJudgeClaude, which puts a `claude` on PATH that writes a fixed verdict —
// because a check whose subject is the judge's WIRING (does the offending code and
// the rule reach the prompt; does a failing verdict block) must be a deterministic
// reproduction, not a race against a model.
//
// The guard is installed FROM the marketplace plugin's own
// .sloprail/file-guard/authoring-slop, verbatim and recursively (the judge-rules/
// subtree is the whole standard the judge assembles), and committed before the
// cycle runs — the engine reads a git diff to decide which paths a cycle changed,
// so the guard's own files must be in history before the fixture write, or the
// judge fires on the guard's own files instead of the fixture.
package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

var (
	New   = harness.New
	Turns = harness.Turns
	Write = harness.Write
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// installAuthoringSlop copies the marketplace plugin's OWN authoring-slop
// file-guard into a test project, verbatim and recursively. The whole subtree —
// file-guard.yaml, check-rules.sh, prepare.sh, judge.md.j2, rules/ and
// judge-rules/ — comes across, so the installed guard is byte-for-byte the one
// shipping in the plugin, judge registry included. It is installed as a PROJECT
// guard (under the project's own .sloprail/file-guard/) so it fires on the
// fixture writes without any plugin-enable dance.
func installAuthoringSlop(t *testing.T, projDir string) {
	t.Helper()
	src := filepath.Join(repoRoot(t), "marketplace", "plugins", "sloprail", ".sloprail", "file-guard", "authoring-slop")
	dst := filepath.Join(projDir, ".sloprail", "file-guard", "authoring-slop")
	copyTree(t, src, dst)
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("copyTree: mkdir %s: %v", dst, err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("copyTree: read %s: %v", src, err)
	}
	if len(entries) == 0 {
		t.Fatalf("copyTree: %s is empty", src)
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyTree(t, s, d)
			continue
		}
		body, err := os.ReadFile(s)
		if err != nil {
			t.Fatalf("copyTree: read %s: %v", s, err)
		}
		info, err := e.Info()
		if err != nil {
			t.Fatalf("copyTree: stat %s: %v", s, err)
		}
		// The mode carries over: a check script arriving without its execute bit is
		// refused by the engine for being unrunnable (fail-closed), and a test would
		// then pass its refusal assertions for the wrong reason.
		if err := os.WriteFile(d, body, info.Mode().Perm()); err != nil {
			t.Fatalf("copyTree: write %s: %v", d, err)
		}
	}
}

// project builds a git-backed project with authoring-slop installed and
// committed, so the guard's own files are in history before the cycle under test.
func project(t *testing.T, e *harness.Env) string {
	t.Helper()
	proj := e.Project()
	e.GitInit(proj)
	installAuthoringSlop(t, proj)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install authoring-slop")
	return proj
}

// runGrepDirect runs the installed check-rules.sh (the GREP) against a script
// body handed as newContent on a PreFileCreate, exactly as the engine would, and
// returns its exit code. This is how a test observes what the grep ALONE decides —
// the floor beneath the judge — so "the grep permits the buggy create" is a fact
// the test checks rather than assumes.
func runGrepDirect(t *testing.T, projDir, scriptBody string) int {
	t.Helper()
	guardDir := filepath.Join(projDir, ".sloprail", "file-guard", "authoring-slop")
	payload, err := json.Marshal(map[string]any{
		"event": map[string]any{
			"path":       ".sloprail/file-guard/mine/check.sh",
			"newContent": scriptBody,
			"kind":       "PreFileCreate",
		},
	})
	if err != nil {
		t.Fatalf("runGrepDirect: marshal payload: %v", err)
	}
	cmd := exec.Command("bash", filepath.Join(guardDir, "check-rules.sh"))
	cmd.Stdin = strings.NewReader(string(payload))
	cmd.Env = append(os.Environ(),
		"SR_GUARDRAIL_DIR="+guardDir,
		"SR_WORKSPACE="+projDir,
	)
	runErr := cmd.Run()
	if runErr == nil {
		return 0
	}
	if ee, ok := runErr.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	t.Fatalf("runGrepDirect: %v", runErr)
	return -1
}
