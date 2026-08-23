package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The gate is the new checkpoint-on-an-event nature: it wakes on a pre-action
// event (or Stop), evaluates require + checks, and blocks or admits. These tests
// drive the compiled sr-session through a10n-claude-mock against a sandboxed
// project holding real .sloprail/gate/*/gate.yaml, so what fires is the plugin's
// own dispatch — a failing script check blocks, a missing skill require blocks,
// a met require with passing checks admits, and the gate's verdict lands in the
// gates[] map a context will read.
var New = harness.New

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

var (
	Turns = harness.Turns
	Write = harness.Write
	Skill = harness.Skill
	Bash  = harness.Bash
)
