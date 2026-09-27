package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// A session's state is keyed on where its conversation began, found by walking
// back across every continuation the harness wrote — compactions, resumes,
// forks. These drive the mock through the continuation shapes real Claude Code
// left on one machine, each of which once left real hook runs with no session
// identity and so no store at all, and assert that what one cycle stored is
// read back by the next cycle of the same conversation.
//
// The observation is the store's own: harness.ControlGuard's check writes a
// mark under its guardrail's state in one hook process and reads it back in the
// next. "before=[yes]" can only be logged by a check that opened the SAME store
// a previous check wrote to.
var (
	New                          = harness.New
	Turns                        = harness.Turns
	Write                        = harness.Write
	Compact                      = harness.Compact
	CompactNamingUnwrittenParent = harness.CompactNamingUnwrittenParent
)

// TestMain removes the binary build dir when this package's tests finish.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}
