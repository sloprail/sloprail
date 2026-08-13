package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The agent is a10n-claude-mock with this repo's plugin enabled, so the Stop
// hook that ends each scenario is the one a user would get. Nothing here
// invokes `sloprail session stop`: the cycle ends, the harness fires Stop, the
// plugin reaches the subcommand, and the guardrails bound to what changed run.
//
// A test that invoked the subcommand itself would prove the engine dispatches
// correctly while proving nothing about whether the end of a cycle ever asks it
// to — which is exactly the gap this hook point had.
var (
	New   = harness.New
	Turns = harness.Turns
	Bash  = harness.Bash
	Write = harness.Write
)

// TestMain removes the binary build dir when this package's tests finish.
// Without it every e2e package leaks 15M for the life of the machine.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}
