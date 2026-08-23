package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The killed-by-signal diagnostic, on the NEW nature format.
//
// A check the OS kills — a segfault, the OOM killer, a stray `kill -9` — reaches
// a dead end from outside, and Go's exec reports it as ExitCode() == -1: the
// sentinel for "died by signal", not a status any process can return. The old
// hook path said "killed" for this; the new check-runner (internal/dispatch
// exec.go) had regressed to "the check refused (exit -1) but gave no reason",
// which sends an author to debug an exit path never taken.
//
// This suite drives the compiled sr-session through a10n-claude-mock against a
// project holding a real .sloprail/file-guard whose PREVENTIVE check kills itself
// with SIGKILL. Preventive puts the check on the pre-tool path, so the refusal it
// produces surfaces in the tool result stream (res.Refused()/res.Saw), where the
// message the author is shown can be asserted. It STILL refuses (fail-closed is
// preserved — a check the OS killed must not read as approval); only the wording
// under test changed.
var New = harness.New

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

var (
	Turns = harness.Turns
	Write = harness.Write
)
