package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// structure_plugin_scopes: structure gates COMBINE. A project's own
// `.sloprail/file-guard/structure.yaml` covers the whole tree; an installed
// plugin's covers only the `scope` it declares; a written path is decided by
// whoever owns it. Every test drives the compiled engine through
// a10n-claude-mock with a synthetic plugin enabled in the project's settings, so
// what fires is the sloprail plugin's own hooks resolving the second plugin and
// loading its structure.yaml — nothing is copied into the project.
var New = harness.New

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

var (
	Turns = harness.Turns
	Write = harness.Write
	Bash  = harness.Bash
)
