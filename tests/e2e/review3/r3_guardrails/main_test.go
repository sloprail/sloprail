// Round-3 adversarial review of the two engine-repo judges, rule-quality and
// skill-quality, which had never been adversarially reviewed.
//
// Everything here drives the REAL guardrail files out of the repo's own
// .sloprail/, through the real engine dispatch, via the harness. Nothing
// restates a declaration or a hook: a test carrying its own copy would prove
// the copy works and say nothing about the files that are actually enforcing.
package e2e

import (
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

// installGuardrail copies one of THIS repo's own .sloprail/guardrails/<name>
// into a test project, verbatim and recursively — the rules/ subtree is the
// whole standard for these two judges, so a copier that skipped directories
// would install a judge with an empty rules/ and every test would observe the
// empty-rules refusal instead of the behaviour under test.
func installGuardrail(t *testing.T, projDir, name string) string {
	t.Helper()

	src := filepath.Join(repoRoot(t), ".sloprail", "guardrails", name)
	dst := filepath.Join(projDir, ".sloprail", "guardrails", name)
	copyTree(t, src, dst)
	return dst
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
		// The mode carries over. A hook script arriving without its execute
		// bit is refused by the engine for being unrunnable, and a test would
		// then pass its refusal assertions for entirely the wrong reason.
		if err := os.WriteFile(d, body, info.Mode().Perm()); err != nil {
			t.Fatalf("copyTree: write %s: %v", d, err)
		}
	}
}

// project builds a git-backed project with one of this repo's own guardrails
// installed and committed.
//
// The commit is not incidental. The engine reads a git diff to decide which
// paths a cycle changed, so a guardrail's own files must already be in history
// before the cycle under test runs — otherwise the guardrail's own RULE.md
// files are part of the cycle's changes and the judge fires on them, which
// turns every assertion below into a statement about the wrong file.
func project(t *testing.T, e *harness.Env, guardrail string) string {
	t.Helper()

	proj := e.Project()
	e.GitInit(proj)
	installGuardrail(t, proj, guardrail)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install "+guardrail)
	return proj
}

// shell runs a shell line and returns its trimmed stdout, for the few
// assertions whose subject IS a shell pipeline rather than a session.
func shell(t *testing.T, line string) string {
	t.Helper()
	out, err := exec.Command("sh", "-c", line).Output()
	if err != nil {
		t.Fatalf("shell %q: %v", line, err)
	}
	return strings.TrimSpace(string(out))
}

// shq single-quotes a string for the shell.
func shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
