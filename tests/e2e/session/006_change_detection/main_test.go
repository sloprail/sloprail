package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The agent is a10n-claude-mock with this repo's plugin enabled, so the Stop
// hook that ends each scenario is the one a user would get. Nothing here
// invokes `sr-session stop`: the cycle ends, the harness fires Stop, the
// plugin reaches the subcommand, and the guardrails bound to what changed run.
//
// A test that invoked the subcommand itself would prove the engine dispatches
// correctly while proving nothing about whether the end of a cycle ever asks it
// to — which is exactly the gap this hook point had.

// Env is the harness environment, aliased so scenario helpers in this package
// can take one without naming the import at every call site.
type Env = harness.Env

var (
	Turns = harness.Turns
	Bash  = harness.Bash
	Write = harness.Write

	CommitRequired = harness.CommitRequired
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
func New(t *testing.T) *Env { return harness.New(t, harness.WithoutShippedFileGuards()) }
