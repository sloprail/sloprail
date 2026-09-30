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
	path := filepath.Join(t.TempDir(), "sub.sh")
	if err := s.Script(path); err != nil {
		t.Fatalf("harness: write sub-agent scenario: %v", err)
	}
	return path
}
