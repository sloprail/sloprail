package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// A gate in a sibling repository B governs the writes and commands that target B, even when
// the session runs in project A. Only B's own declarations load; its plugins are not applied.
var (
	New   = harness.New
	Turns = harness.Turns
	Bash  = harness.Bash
	Write = harness.Write
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}
