package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// commit_required: at Stop, an uncommitted change to a path some file-guard's
// match selects refuses the turn — "commit these". Always on, never commits for
// the agent, in the harness's real blocking form, only for an agent that owns the
// tree, with a loop breaker in the spirit of stop_hook_block_cap.

type Env = harness.Env

var (
	// New opts in to a sub-agent's own Stop verifying (the default is off): this package tests it.
	New = func(t *testing.T, o ...harness.Option) *harness.Env {
		// The rule-tests-* rules judge the sr-test cases of every rule a range adds: the rules these tests
		// commit are no subject of theirs.
		return harness.New(t, append(o, harness.WithSubagentStopCheck(),
			harness.WithoutShipped("sloprail/file-guard/rule-tests-pass", "sloprail/file-guard/rule-tests-rigorous"))...)
	}
	Turns    = harness.Turns
	Bash     = harness.Bash
	Write    = harness.Write
	Dispatch = harness.Dispatch
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// rule is a file-guard over docs/ whose check always passes and never matters:
// commit-required is about what the rule SELECTS, not what its check thinks.
const rule = "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n"

const passing = "#!/bin/sh\ncat >/dev/null\nexit 0\n"

// project is a committed repository with docs/seed.md and a committed rule.
func project(t *testing.T) (*Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.WriteFile(proj, "notes/scratch.md", "scratch\n")
	e.FileGuard(proj, "docs", rule, map[string]string{"check.sh": passing})
	e.CommitAll(proj, "the project and its rule")
	return e, proj
}
