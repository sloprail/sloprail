package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// unreadable_committed_files: a file-guard over a range holding a committed file
// it cannot sensibly read as text — a symlink whose target never ends
// (/dev/zero), a file too large to hold in memory — must not hang the Stop, and
// must not wave the range through: the rule is either handed what git holds or the
// Stop is refused with the reason.
//
// Run as `env -u CLAUDECODE -u CLAUDE_CODE_SESSION_ID go test ...`.

type Env = harness.Env

var (
	New   = harness.New
	Turns = harness.Turns
	Bash  = harness.Bash
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}
