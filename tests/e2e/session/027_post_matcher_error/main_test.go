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

// project is a repository whose guardrails are committed before the session, so
// the rules' own folders are part of the baseline rather than being reported as
// files this cycle created.
func project(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	return e, proj
}

func commitGuardrails(e *harness.Env, proj string) {
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")
}
