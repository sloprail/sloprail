package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The agent is a10n-claude-mock with this repo's plugin enabled, so what fires
// during a test is the wiring a user would get. These tests drive the SHIPPED
// no-unasked-deletion example (examples/no-unasked-deletion/.sloprail), installed
// verbatim, so a green run means those files work — including the grounded-ask
// path the shipped script now grounds with `cite --path "$transcript_path"`.
type env = harness.Env

var (
	newEnv = harness.New
	Turns  = harness.Turns
	Write  = harness.Write
	Bash   = harness.Bash
)

// srWrite is the agent replacing a file's whole content with sr-file, citing the
// user's words — the grounded way to make a change that removes content. The
// quote rides on the command; the file keeps only its own content.
func srWrite(id, path, content, quote string) harness.Turn {
	return Bash(id, "sr-file write "+path+" --cite:user "+shq(quote)+" --content "+shq(content))
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// TestMain removes the binary build dir when this package's tests finish.
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

// installExampleTree copies the WHOLE examples/<name>/.sloprail tree into a
// project, preserving each file's mode bits and recreating subdirectories. Read
// off disk rather than restated as consts: examples are truth.
func installExampleTree(t *testing.T, projDir, name string) {
	t.Helper()
	src := filepath.Join(repoRoot(t), "examples", name, ".sloprail")
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

// (This package asserts on res.Refused()/res.Saw() and e.Exists(), so it needs no
// blocking-error helpers; the guard is preventive and refuses at pre-tool.)
