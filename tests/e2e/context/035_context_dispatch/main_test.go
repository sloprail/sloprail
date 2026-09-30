package e2e

import (
	"os"
	"os/exec"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The context is the activatable-scope nature: its `enter` runs on every matching
// `on` trigger and its stdout replaces the context's payload; its `exit` runs at
// Stop and flips active/inactive WITHOUT blocking the Stop (the reversal). These
// tests drive the compiled sr-session through a10n-claude-mock against a sandboxed
// project holding real .sloprail/context/<name>/context.yaml, so what fires is the
// plugin's own lifecycle dispatch:
//
//   - a context ENTERS on its trigger and its state shows active (and its payload)
//     in a later guard's / gate's match;
//   - a context's EXIT runs at Stop and does NOT block it (a gate does);
//   - a context's payload is readable by a gate's require:[{context}];
//   - the eval-loop-maxing composite (a goal-tracking context + a goal-verify gate
//     reading the goal) works end to end.
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
	Skill = harness.Skill
)

// commitGuards commits the project's `.sloprail` tree so a context/file-guard
// installed after the baseline is part of it, not the first cycle's diff.
//
// The sloprail plugin ships authoring-slop, a gate plus a file-guard whose Stop
// after-check judges a guardrail's own `.sh` machinery. A context's enter/exit
// scripts and a file-guard's check.sh installed here are uncommitted, so the Stop
// after-check would read them as this cycle's writes and judge them — and with no
// model wired in the e2e that judge fails closed, adding spurious blocking errors
// to a test that expected a clean Stop. Committing the tree (as production does,
// where guards are installed before the session) keeps it out of the cycle diff.
// Scoped to `.sloprail` so it never sweeps in the src/ files these tests write.
func commitGuards(t *testing.T, proj string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", proj, "add", ".sloprail").CombinedOutput(); err != nil {
		t.Fatalf("commitGuards: git add .sloprail: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", proj, "commit", "-m", "baseline .sloprail").CombinedOutput(); err != nil {
		t.Fatalf("commitGuards: git commit: %v\n%s", err, out)
	}
}

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
