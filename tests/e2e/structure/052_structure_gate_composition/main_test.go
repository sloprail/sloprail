package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The structure gate stopped being a singleton the project and its plugins
// compete over (pick-one-winner, the rest Shadowed) and became a COMPOSITION:
// every enabled plugin's own scoped structure gate stays in force alongside the
// project's, each owning the slice of the tree its `scope` names. These tests
// drive the compiled sr-session through a10n-claude-mock against a sandboxed
// project with a synthetic plugin installed (harness.EnablePluginShippingStructureGate,
// the structure-gate analogue of 026's EnablePluginShippingFileGuard), so what
// fires is the real plugin-loading path, not a hand-built declaration set.
var New = harness.New

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

var (
	Turns = harness.Turns
	Write = harness.Write
)
