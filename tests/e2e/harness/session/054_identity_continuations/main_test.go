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
// next. "before=[yes]" can only be logged by a check that opened a store some
// earlier check wrote to — so each test requires EVERY probe run of the
// continuation's cycle to read it (readsBack), since a second run within one
// cycle would read the first's mark from a brand-new store. Each test also
// asks the engine which identity each transcript resolves to.
var (
	Turns                        = harness.Turns
	Write                        = harness.Write
	Bash                         = harness.Bash
	Compact                      = harness.Compact
	CompactNamingUnwrittenParent = harness.CompactNamingUnwrittenParent
)

// TestMain removes the binary build dir when this package's tests finish.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// New is harness.New with the plugin's authoring file-guards switched off: this package
// is about other rules, and the authoring guards would judge the rules' own files.
func New(t *testing.T) *harness.Env { return harness.New(t, harness.WithoutShippedFileGuards()) }
