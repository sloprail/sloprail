package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// PostTagWrite through the wiring a user gets: the one bulk event carrying every
// `#tag` the agent wrote into its own messages this cycle, so a context can bind
// a tag directly instead of re-grepping the trajectory in its own enter script.
var (
	New   = harness.New
	Turns = harness.Turns
	Say   = harness.Say
	Write = harness.Write
)

// TestMain removes the binary build dir when this package's tests finish.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}
