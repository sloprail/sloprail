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
// grounding-citations example (examples/grounding-citations/.sloprail),
// installed verbatim, so a green run means those files work.
type env = harness.Env

var (
	newEnv = harness.New
	Turns  = harness.Turns
	Write  = harness.Write
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

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// installExampleTree copies the WHOLE examples/<name>/.sloprail tree into a
// project VERBATIM, preserving each file's mode bits (a hook script ships
// executable, so it must arrive executable — the engine fail-closed-refuses a
// non-executable check) and recreating subdirectories. Read off disk rather than
// restated as consts: examples are truth, and a test holding its own copy would
// drift from the file a user lifts.
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

	// Commit the installed tree so it is part of the session baseline, not the
	// first cycle's diff. The sloprail plugin ships authoring-slop, a gate and
	// a file-guard whose Stop after-check judges a guardrail's own `.sh`/`.md.j2`
	// machinery; an uncommitted example tree reads as this cycle's writes, so that
	// after-check would judge the example's own scripts and, with no model in the
	// e2e, fail closed. Production installs before the session (baseline), so it is
	// never in the cycle diff — this reproduces that. No-op when proj is not a repo.
	harness.CommitInstalled(t, projDir)
}

// joinBlocks renders a slice of blocking-error texts for a log/assert message.
func joinBlocks(bs []string) string { return strings.Join(bs, "\n---\n") }

// containsAll reports whether every needle appears in haystack.
func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			return false
		}
	}
	return true
}

// fileGuardLedger counts the lines a file-guard's check appended to a file in its
// own folder — how many times the check was ASKED. Absent means it never ran. The
// shipped example writes no ledger, so this reads one only for the re-fire test,
// which installs a ledger-writing check of the same `**/*.md` shape.
func fileGuardLedger(t *testing.T, projDir, guardName, file string) int {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(projDir, ".sloprail", "file-guard", guardName, file))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read file-guard ledger %s/%s: %v", guardName, file, err)
	}
	n := 0
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// newEnvUncited is newEnv with the commit-time sloprail/gate/cite-before-commit switched off, for a scenario about
// what Stop or `sr-checks run` does with a commit that carries no (or no resolving) citation: with
// the gate on, the agent could not make that commit at all. The gate is exercised in
// tests/e2e/gate/058_cite_before_commit.
func newEnvUncited(t *testing.T) *harness.Env {
	return harness.New(t, harness.WithoutShipped("sloprail/gate/cite-before-commit"))
}
