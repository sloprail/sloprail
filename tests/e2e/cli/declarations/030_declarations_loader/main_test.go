package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The `sr-file declarations` command loads a project's new-format `.sloprail`
// declarations and reports what loaded and what did not. These tests drive the
// compiled binary directly (not through the mock session), because what is under
// test is the command's own contract: its stdout, its per-fault reporting, and
// the exit status a hook or CI step reads.
var New = harness.New

// TestMain removes the binary build dir when this package's tests finish, the
// same as every other e2e package — without it each leaks the built binaries for
// the life of the machine.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}
