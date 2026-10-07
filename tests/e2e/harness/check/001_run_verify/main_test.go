package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// run_verify: file-guards are judged on an EXPLICIT range with `sr check run|verify
// --base --head`. No session, branch or earlier run is tracked: a judge's verdict is keyed by
// the rule, its definition, the check and a fingerprint of the content it was given, so the
// same content after a rebase, a squash or a revert is a cache hit and changed content is
// judged. Driven through the compiled binary against a sandboxed repository, with the model
// stood in for by the claude shim, which counts its calls.

type Env = harness.Env

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

const (
	judgeRule = "match: \"docs/**\"\nchecks:\n  - judge: ./rubric.md.j2\n"
	rubric    = "Does this change to the docs hold up?\n{{ change }}\n"

	verdictPass = `{"pass": true, "reasoning": "fine"}`
	verdictFail = `{"pass": false, "reasoning": "JUDGE-SAYS-NO: the doc contradicts itself"}`
	promptFile  = ".git/judge-prompt"

	session = "s-check"
)

// scriptRule refuses a changed docs file containing FORBIDDEN.
const scriptRule = "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n"

const forbiddenCheck = `#!/bin/sh
payload="$(cat)"
if printf '%s' "$payload" | jq -e 'any(.changeset.files[]; .newContent | contains("FORBIDDEN"))' >/dev/null; then
  echo '{"reason":"FORBIDDEN text in the changeset"}'
  exit 1
fi
exit 0
`

// project is a repository with one commit, then a committed `docs` rule. Returns the env,
// the project and the rule's commit: the base of every range a test judges.
func project(t *testing.T, ruleYAML string, files map[string]string, verdict string) (*Env, string, string) {
	t.Helper()
	e := harness.New(t, harness.WithoutShippedFileGuards(), harness.NoAutoCheck())
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project")
	e.FileGuard(proj, "docs", ruleYAML, files)
	e.CommitAll(proj, "the rule")
	if verdict != "" {
		e.InstallJudgeClaudeCapturing(proj, promptFile, verdict)
	}
	return e, proj, e.Git(proj, "rev-parse", "HEAD")
}

func judged(e *Env, proj string) int { return e.JudgeCalls(proj, promptFile, "") }

func run(e *Env, proj, base, head string) harness.Result {
	return e.CheckRunRaw(proj, session, base, head)
}

func verify(e *Env, proj, base, head string) harness.Result {
	return e.CheckVerify(proj, session, base, head)
}

func commitDoc(e *Env, proj, path, body, msg string) {
	e.WriteFile(proj, path, body)
	e.CommitAll(proj, msg)
}

func mustContain(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Fatalf("output lacks %q:\n%s", w, got)
		}
	}
}
