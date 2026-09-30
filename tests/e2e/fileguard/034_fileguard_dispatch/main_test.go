package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The file-guard is the nature bound to a FILE'S STATE (its `match` over
// path/markers/context), not to an event trigger. These tests drive the compiled
// sr-session through a10n-claude-mock against a sandboxed project holding real
// .sloprail/file-guard/<name>/file-guard.yaml, so what fires is the plugin's own
// dispatch:
//
//   - a not-fine file blocks the TURN at Stop (the after-check, over the
//     committed range) and keeps refusing until it is fixed — the refused range does not move, so the fix is judged
//     together with what it fixes;
//   - a file-guard never acts before a write: prevention is a PreFileWrite /
//     PreFileDelete GATE, which blocks a not-fine write at pre-tool BEFORE it
//     lands, is asked about every file of a call, and fails CLOSED (its check's
//     job) when the engine could not compute the write's result;
//   - a guard only judges the files its `match` selects.
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

// containsStr is a tiny local substring helper, so a test can assert on refusal
// text without importing strings in every file.
func containsStr(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
