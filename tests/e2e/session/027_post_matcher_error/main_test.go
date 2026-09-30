package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

var (
	New   = harness.New
	Turns = harness.Turns
	Bash  = harness.Bash
	Write = harness.Write
)

// TestMain removes the binary build dir when this package's tests finish.
// Without it every e2e package leaks 15M for the life of the machine.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// project is a repository whose new-format rules are committed before the session,
// so the rules' own folders under .sloprail/ are part of the baseline rather than
// being reported as files this cycle created — which matters for the file-guard
// after-checks here, whose Post events are computed from the cycle's tree
// difference and would otherwise include the guard's own .sloprail/file-guard/…
// files (and `int(path) > 0` errors on every path, the guard's own included).
func project(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	return e, proj
}

// commitProject commits the whole project — including the new-format
// .sloprail/{gate,file-guard}/… declarations — so they are the baseline and not
// the cycle's own work. The new-format equivalent of the old-format
// commitGuardrails, which committed .sloprail/guardrails/….
func commitProject(e *harness.Env, proj string) {
	e.CommitAll(proj, "the project before the session")
}
