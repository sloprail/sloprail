package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// `init` and `guardrail help` are typed rather than invoked by a harness, so
// these drive the binary directly. Everything a session triggers is tested
// through the mock instead — see tests/e2e/pre_tool.
var (
	New   = harness.New
	Turns = harness.Turns
	Write = harness.Write
)

// TestMain removes the binary build dir when this package's tests finish.
// Without it every e2e package leaks 15M for the life of the machine.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}
