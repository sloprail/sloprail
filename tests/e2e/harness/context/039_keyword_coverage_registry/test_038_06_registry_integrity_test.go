package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The registry is the whole enforcement: verify-scanner-coverage holds every
// search to what scanner-declared logged, and search-needs-declared-scanner lets
// a search run only once something is logged. So each way the registry could
// shrink, merge, stick, or be misread is a way past both gates. Each test here is
// one such way, found by review of the shipped example and reproduced first.

// researchProjectWithScanner is researchProject with activeScanner (guardrail,
// llm, agent) committed at scanners/mine — a scanner that predates the session.
func researchProjectWithScanner(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e, proj := researchProject(t)
	e.WriteFile(proj, "scanners/mine/scanner.yaml", activeScanner)
	e.CommitAll(proj, "scanner")
	return e, proj
}
