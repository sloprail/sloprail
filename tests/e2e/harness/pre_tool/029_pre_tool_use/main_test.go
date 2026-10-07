package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// PreToolUse through the wiring a user gets: the harness-native pre-action
// moment, ahead of any file or command derivation, that a rule binds to when it
// needs to gate a tool neither a file event nor a command event covers.
var (
	New   = harness.New
	Turns = harness.Turns
	Skill = harness.Skill
	Bash  = harness.Bash
)

// TestMain removes the binary build dir when this package's tests finish.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}
