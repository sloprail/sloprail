package e2e

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// tracked_ranges: tracking lives in the SESSION, the checks stay stateless. A session registers
// the folders it works in and, per folder, the ranges of commits it answers for: when a folder
// is discovered its current branch is tracked from where the work started (the merge base with
// the default branch); the agent may track another range or drop one with a reason. At Stop each
// tracked range is VERIFIED against the stored check results — never judged: a range whose judges
// have not been asked is refused with the `sr-checks run` that asks them.

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

var (
	Turns = harness.Turns
	Bash  = harness.Bash
)

type Env = harness.Env

const (
	judgeRule  = "match: \"docs/**\"\nchecks:\n  - judge: ./rubric.md.j2\n"
	rubric     = "Does this change to the docs hold up?\n{{ change }}\n"
	promptFile = ".git/judge-prompt"
)

// project is a committed repository with origin and a committed judged `docs` rule. The harness
// does not run the checks before Stop (NoAutoCheck): these tests are about what Stop does with
// a range nobody has judged.
func project(t *testing.T) (*Env, string) {
	t.Helper()
	e := harness.New(t, harness.WithoutShippedFileGuards(), harness.NoAutoCheck())
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "docs", judgeRule, map[string]string{"rubric.md.j2": rubric})
	e.CommitAll(proj, "the rule")
	e.InstallJudgeClaudeCapturing(proj, promptFile, `{"pass": true, "reasoning": "fine"}`)
	return e, proj
}

// ranges is `sr-session refs list --json` as the session.
func ranges(t *testing.T, e *Env, proj, sess string) []sessionstate.TrackedRange {
	t.Helper()
	r := e.CLIDirectEnv(proj, e.SessionEnv(sess), "sr-session", "refs", "list", "--json")
	if r.Code != 0 {
		t.Fatalf("refs list: exit %d:\n%s", r.Code, r.Output)
	}
	var out []sessionstate.TrackedRange
	if err := json.Unmarshal([]byte(r.Output), &out); err != nil {
		t.Fatalf("refs list --json is not JSON (%v):\n%s", err, r.Output)
	}
	return out
}

func refs(e *Env, proj, sess string, args ...string) harness.Result {
	return e.CLIDirectEnv(proj, e.SessionEnv(sess), "sr-session", append([]string{"refs"}, args...)...)
}
