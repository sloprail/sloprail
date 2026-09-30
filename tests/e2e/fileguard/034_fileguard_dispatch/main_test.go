package e2e

import (
	"os"
	"os/exec"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The file-guard is the nature bound to a FILE'S STATE (its `match` over
// path/markers/context), not to an event trigger. These tests drive the compiled
// sr-session through a10n-claude-mock against a sandboxed project holding real
// .sloprail/file-guard/<name>/file-guard.yaml, so what fires is the plugin's own
// dispatch:
//
//   - a not-fine file blocks the TURN at Stop (the after-check) and RE-FIRES next
//     cycle until it is fixed — the file-guard's defining "re-fires until fine";
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

// commitGuards commits the project's `.sloprail` tree so a guard's own check.sh /
// judge.md.j2 — installed after the baseline — is part of it, not the first
// cycle's diff. The sloprail plugin ships authoring-slop, a gate and a file-guard
// whose Stop after-check judges a guardrail's own `.sh`/`.md.j2`; an uncommitted
// one reads as this cycle's write and is judged (failing closed with no model in
// the e2e, adding a spurious block to a test that expected a clean Stop).
// Production installs guards before the session (baseline), so committing keeps
// them out of the cycle diff. Scoped to `.sloprail` so it never sweeps in the
// memories/, docs/ files these tests write. Requires the project be a git repo
// (every test here GitInits before installing).
func commitGuards(t *testing.T, proj string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", proj, "add", ".sloprail").CombinedOutput(); err != nil {
		t.Fatalf("commitGuards: git add .sloprail: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", proj, "commit", "-m", "baseline .sloprail").CombinedOutput(); err != nil {
		t.Fatalf("commitGuards: git commit: %v\n%s", err, out)
	}
}

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
