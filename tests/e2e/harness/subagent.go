package harness

import (
	"path/filepath"
	"testing"
)

// SubagentScript writes a scenario the mock runs as a sub-agent and returns its
// path, for Dispatch.
//
// Outside the project tree deliberately. A script written into the project is an
// untracked file in the tree the cycle diffs, so it turns up as a change the
// guardrails are asked about — noise in every assertion about what a cycle judged
// — and in an isolated dispatch it is not even in the sub-agent's tree.
func SubagentScript(t testing.TB, s Scenario) string {
	t.Helper()
	// The turn a real sub-agent takes before it stops: judge the ranges of its own worktree,
	// which its SubagentStop will verify.
	s.turns = append(append([]Turn{}, s.turns...), Bash("srsubprestop", mustDriver().SubagentSessionExport()+runTrackedRanges(true)))
	return writeSubagentScript(t, s)
}

// SubagentScriptUnjudged is SubagentScript without that turn: a sub-agent that stops with its
// committed range never judged, for a test of what the Stop says to a range nobody judged.
func SubagentScriptUnjudged(t testing.TB, s Scenario) string {
	t.Helper()
	return writeSubagentScript(t, s)
}

func writeSubagentScript(t testing.TB, s Scenario) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub.sh")
	if err := s.Script(path); err != nil {
		SkipIfUnsupported(t, err)
		t.Fatalf("harness: write sub-agent scenario: %v", err)
	}
	return path
}
