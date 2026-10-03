package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The plugin's sloprail/gate/ci-verify-required (on by default) refuses a Stop in a project that
// loads file-guards until the committed tree carries a line containing `sr-mark: ci-verify`, the
// marker for a CI job that runs `sr-checks verify`. Every other e2e package has it disabled in its
// initial commit (harness.WithCIVerifyRequired is the opt-in used here).

type Env = harness.Env

var Turns = harness.Turns

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

const marker = "sr-mark: ci-verify"

// passCheck is a file-guard check that accepts everything: this package is about whether the
// file-guard exists, not what it judges.
const passCheck = "#!/bin/sh\ncat >/dev/null\nexit 0\n"

// project is a committed repository with the gate on. withGuard adds a project file-guard over
// docs/** (the shipped authoring file-guards stay off either way, so "no file-guards" is exact).
func project(t *testing.T, withGuard bool) (*Env, string) {
	t.Helper()
	e := harness.New(t, harness.WithoutShippedFileGuards(), harness.WithCIVerifyRequired())
	e.SetStopBlockCap(1)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project")
	if withGuard {
		e.FileGuard(proj, "docs", "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n", map[string]string{"check.sh": passCheck})
		e.CommitAll(proj, "the rule")
	}
	return e, proj
}

// stop runs one turn that changes nothing and returns the Stop refusals the agent was shown by
// THAT turn (the session's record is cumulative, so earlier turns' refusals are not repeated).
func stop(e *Env, proj, sess string) string {
	before := len(e.StopContinuations(proj, sess))
	e.Run(proj, sess, "do nothing", Turns("done"))
	return strings.Join(e.StopContinuations(proj, sess)[before:], "\n")
}
