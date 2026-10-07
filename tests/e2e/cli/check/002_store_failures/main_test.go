package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// store_failures: a `sloprail/checks` ref the build cannot read (a corrupt segment, or a
// directory written by a newer schema) is an error to `sr-checks run` and `verify`, never a
// "not judged yet" and never a reason to ask the judge again.

type Env = harness.Env

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

const (
	judgeRule = "match: \"docs/**\"\nsubjects: ./subjects.sh\nchecks:\n  - judge: ./rubric.md.j2\n"
	rubric    = "Does this change to the docs hold up?\n{{ change }}\n"
	// subjectsScript names one subject, "api", over docs/a.md, with a fingerprint of its own.
	subjectsScript = "#!/bin/sh\ncat >/dev/null\necho '[{\"id\":\"api\",\"files\":[\"docs/a.md\"],\"fingerprint\":\"schema-v1\"}]'\n"

	promptFile = ".git/judge-prompt"
	currentDir = "v2026-10-08"
	noJudges   = "SR_CHECKS_JUDGE_MOCKS={}"
)

// fixture is a repository with the judged, subject-split rule, one docs/a.md change after it,
// and the range base..head.
type fixture struct {
	e          *Env
	proj       string
	base, head string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	e := harness.New(t, harness.WithoutShippedFileGuards(), harness.NoAutoCheck())
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project")
	e.FileGuard(proj, "docs", judgeRule, map[string]string{"rubric.md.j2": rubric, "subjects.sh": subjectsScript})
	base := e.CommitAll(proj, "the rule")
	e.WriteFile(proj, "docs/a.md", "the release is Friday\n")
	head := e.CommitAll(proj, "add a")
	return &fixture{e: e, proj: proj, base: base, head: head}
}

func (f *fixture) env() []string {
	return append(append([]string{}, harness.NoSessionEnv...), noJudges)
}

func (f *fixture) verify(base, head string) harness.Result {
	return f.e.CLIDirectEnv(f.proj, f.env(), "sr-checks", "verify", "--base", base, "--head", head)
}

func (f *fixture) judgeCalls() int { return f.e.JudgeCalls(f.proj, promptFile, "") }
