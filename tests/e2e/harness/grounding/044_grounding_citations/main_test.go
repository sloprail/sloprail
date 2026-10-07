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
// grounding-citations example (testdata/grounding-citations/sloprail),
// installed verbatim, so a green run means those files work.
type env = harness.Env

var (
	newEnv = harness.New
	Turns  = harness.Turns
	Bash   = harness.Bash
)

// shq single-quotes s for a POSIX shell, so a Bash turn passes it verbatim.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// citeTool renders one sr-file citation of a tool's output.
func citeTool(quote string) string { return "--cite:tool_result " + shq(quote) }

// readSource is the tool run that puts a source's text on the transcript, where a
// write can cite it as tool output.
func readSource(id, path string) harness.Turn { return Bash(id, "cat "+shq(path)) }

// srWrite is a Bash turn that writes path with sr-file, citations on the command.
// Run on its own in the line, so the pre-tool hook resolves it exactly.
func srWrite(id, path, content string, cites ...string) harness.Turn {
	return Bash(id, "sr-file write "+path+" --content "+shq(content)+" "+strings.Join(cites, " "))
}

// TestMain removes the binary build dir when this package's tests finish.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// The fixture under testdata/ is a verbatim copy of sloprail-community examples/<name>
// (its `.sloprail` dir renamed `sloprail`).
//
// installExampleTree copies the WHOLE testdata/<name>/sloprail tree into a
// project VERBATIM, preserving each file's mode bits (a hook script ships
// executable, so it must arrive executable — the engine fail-closed-refuses a
// non-executable check) and recreating subdirectories. Read off disk rather than
// restated as consts: examples are truth, and a test holding its own copy would
// drift from the file a user lifts.
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

	// Commit the installed tree so it is part of the session baseline, not the
	// first cycle's diff. The sloprail plugin ships authoring-slop, a gate and
	// a file-guard whose Stop after-check judges a guardrail's own `.sh`/`.md.j2`
	// machinery; an uncommitted example tree reads as this cycle's writes, so that
	// after-check would judge the example's own scripts and, with no model in the
	// e2e, fail closed. Production installs before the session (baseline), so it is
	// never in the cycle diff — this reproduces that. No-op when proj is not a repo.
	harness.CommitInstalled(t, projDir)
}
