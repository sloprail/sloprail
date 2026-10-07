package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The structure gate is the new tree-wide path allowlist, deny-by-default. These
// tests drive the compiled sr-session through a10n-claude-mock against a sandboxed
// project holding a real .sloprail/file-guard/structure.yaml, so what fires is the
// plugin's own dispatch — a write outside the allowlist is refused before it lands,
// a write inside is permitted, and a deny exception carves a path back out.
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
