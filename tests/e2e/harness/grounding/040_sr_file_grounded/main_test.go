package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// sr_file_grounded: `sr-file write|edit|delete` as commands in their own right.
//
// Each test first drives a real session through the mock so a genuine transcript
// holds the user's words, then runs sr-file directly against it (SR_TRANSCRIPT
// names the record, as the hook does). It pins the Write/Edit semantics, that a
// citation which does not resolve writes NOTHING, and resolve mode: with
// SR_FILE_RESOLVE_DIR set the file is untouched and the change is recorded there.
var New = harness.New

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

var (
	Turns = harness.Turns
	Bash  = harness.Bash
)
