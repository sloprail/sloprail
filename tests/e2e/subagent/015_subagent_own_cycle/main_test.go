package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

var (
	Turns    = harness.Turns
	Write    = harness.Write
	Bash     = harness.Bash
	Dispatch = harness.Dispatch
)

// TestMain removes the binary build dir when this package's tests finish.
// Without it every e2e package leaks 15M for the life of the machine.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// New is harness.New with the plugin's authoring file-guards switched off: this package
// is about other rules, and the authoring guards would judge the rules' own files.
func New(t *testing.T) *harness.Env { return harness.New(t, harness.WithoutShippedFileGuards()) }
