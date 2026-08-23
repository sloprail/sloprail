package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Env is the harness environment, aliased so scenario helpers in this package
// can take one without naming the import at every call site.
type Env = harness.Env

var (
	New   = harness.New
	Turns = harness.Turns
	Write = harness.Write
	Bash  = harness.Bash
)

// writeFile puts a file into the project directly, without going through the
// agent — the project's own pre-existing content, as opposed to anything the
// cycle did.
func writeFile(t *testing.T, projDir, name, content string) {
	t.Helper()
	path := filepath.Join(projDir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("seed %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed %s: %v", name, err)
	}
}

// commitGuards commits the project's `.sloprail` tree so the "asker" file-guard's
// own ask.sh — installed after the baseline — is part of it, not the first cycle's
// diff. The sloprail plugin ships authoring-slop, a preventive file-guard whose
// Stop after-check judges a guardrail's own `.sh`; an uncommitted ask.sh reads as
// this cycle's write and is judged (failing closed with no model in the e2e,
// adding spurious blocking errors). Production installs guards before the session
// (baseline), so committing keeps ask.sh out of the cycle diff. Scoped to
// `.sloprail` so it never sweeps in the *.md files these tests write.
func commitGuards(t *testing.T, proj string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", proj, "add", ".sloprail").CombinedOutput(); err != nil {
		t.Fatalf("commitGuards: git add .sloprail: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", proj, "commit", "-m", "baseline .sloprail").CombinedOutput(); err != nil {
		t.Fatalf("commitGuards: git commit: %v\n%s", err, out)
	}
}

// TestMain removes the binary build dir when this package's tests finish.
// Without it every e2e package leaks 15M for the life of the machine.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}
