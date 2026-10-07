package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The plugin's sloprail/gate/no-lifecycle-commands refuses an agent's Bash invocation of the
// harness-only entry points of sr-session (start, stop, pre-tool, subagent-stop). It ships on
// by default, so these tests install nothing: the plugin's own gate fires.
var (
	Turns = harness.Turns
	Bash  = harness.Bash
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}
