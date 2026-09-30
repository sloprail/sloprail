package e2e

import (
	"os"
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
