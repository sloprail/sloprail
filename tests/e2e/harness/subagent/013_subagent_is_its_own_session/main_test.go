package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

var (
	// New opts in to a sub-agent's own Stop verifying (the default is off): this package tests it.
	New = func(t *testing.T, o ...harness.Option) *harness.Env {
		return harness.New(t, append(o, harness.WithSubagentStopCheck())...)
	}
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
