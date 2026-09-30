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
	Turns = harness.Turns
	Bash  = harness.Bash
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// New is harness.New with the plugin's authoring file-guards switched off: this package
// is about other rules, and the authoring guards would judge the rules' own files.
func New(t *testing.T) *harness.Env { return harness.New(t, harness.WithoutShippedFileGuards()) }
