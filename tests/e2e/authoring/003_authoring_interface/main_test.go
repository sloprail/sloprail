package e2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The load check is typed by an authoring agent rather than invoked by a
// harness, so it drives the binary directly. Everything a session triggers is
// tested through the mock instead — see tests/e2e/pre_tool.
//
// The scenario builders are here for T003_11/T003_12, which have to drive a real
// session: proving the skill is enough to author against means running what was
// authored, and a rule that only loads has not been shown to do anything.
var (
	New   = harness.New
	Turns = harness.Turns
	Write = harness.Write
	Bash  = harness.Bash
)

// TestMain removes the binary build dir when this package's tests finish.
// Without it every e2e package leaks 15M for the life of the machine.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// repoRoot is the module root, asked of the toolchain rather than derived from
// this test's own path.
func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("locate the module root: %v", err)
	}
	return strings.TrimSpace(string(out))
}
