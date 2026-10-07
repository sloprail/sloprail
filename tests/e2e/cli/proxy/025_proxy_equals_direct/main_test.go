package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The root `sr` proxy exists so a person has one name to learn, and it is only
// worth having if going through it is indistinguishable from naming the service
// binary. These tests drive both binaries directly rather than through the mock:
// what is under test is the process boundary itself — argv, the streams, and the
// exit status — not anything a session does.
var New = harness.New

// TestMain removes the binary build dir when this package's tests finish.
// Without it every e2e package leaks 15M for the life of the machine.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}
