package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The agent is a10n-claude-mock with this repo's plugin enabled, so what fires
// during a test is the wiring a user would get. These tests drive the SHIPPED
// no-unasked-commit example (testdata/no-unasked-commit/sloprail), installed
// verbatim, never a copy embedded in this test file — a test carrying its own
// copy of the rule would keep passing after the shipped one broke.
type env = harness.Env

var (
	newEnv = harness.New
	Turns  = harness.Turns
	Bash   = harness.Bash
	Write  = harness.Write
)

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// TestMain removes the binary build dir when this package's tests finish.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// The fixture under testdata/ is a verbatim copy of sloprail-community examples/no-unasked-commit.
// installExampleTree copies the WHOLE testdata/<name>/sloprail tree into a
// project, preserving each file's mode bits and recreating subdirectories. Read
// off disk rather than restated as consts: examples are truth.
func installExampleTree(t *testing.T, projDir, name string) {
	t.Helper()
	src := filepath.Join("testdata", name, "sloprail")
	dst := filepath.Join(projDir, ".sloprail")
	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		t.Fatalf("install example tree: %s is not a directory (%v)", src, err)
	}
	walkErr := filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, body, fi.Mode().Perm())
	})
	if walkErr != nil {
		t.Fatalf("install example tree %s: %v", name, walkErr)
	}
}

// nucProject stands up a project with the no-unasked-commit example installed
// verbatim (its scripts ship executable, so no chmod is needed).
func nucProject(t *testing.T, e *env) string {
	t.Helper()
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, "no-unasked-commit")
	return proj
}

// (This package asserts on res.Refused()/res.Saw() and e.Exists(), so it needs no
// blocking-error helpers; the gate fires at pre-tool on PreCommandInvoke.)
