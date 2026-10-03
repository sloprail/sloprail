package e2e

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// sr_checks: `sr-checks show --base --head` — reading what the file-guards concluded about a
// range of commits. Driven through the COMPILED binary against results `sr-checks run` stored
// (the writer is `run`; what is under test here is the reader, which must only read).

type Env = harness.Env

var (
	New   = harness.New
	Turns = harness.Turns
	Bash  = harness.Bash
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

const sessionID = "s-checks-001"

// session is a project with a session the mock has run, so the engine knows its
// identity and where its check results belong.
func session(t *testing.T) (*Env, string) {
	t.Helper()
	e := New(t, harness.WithoutShippedFileGuards(), harness.NoAutoCheck())
	proj := e.Project()
	e.GitInit(proj)
	e.Run(proj, sessionID, "hello", Turns("done", Bash("b1", "true")))
	return e, proj
}

// checks runs `sr-checks <args>` in the session, without the engine's timing lines.
func checks(e *Env, proj string, args ...string) harness.Result {
	return quiet(e.CLIDirectEnv(proj, e.SessionEnv(sessionID), "sr-checks", args...))
}

// quiet drops the engine's "sloprail: ... evaluated in" timing diagnostics, which differ run to run.
func quiet(r harness.Result) harness.Result {
	var keep []string
	for _, l := range strings.SplitAfter(r.Output, "\n") {
		if !strings.HasPrefix(l, "sloprail: file-guard") {
			keep = append(keep, l)
		}
	}
	r.Output = strings.Join(keep, "")
	return r
}

// Row is one `sr-checks show --json` row.
type Row struct {
	Rule    string `json:"rule"`
	Subject string `json:"subject"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Source  string `json:"source"`
	Reason  string `json:"reason"`
}

func statusRows(t *testing.T, e *Env, proj string, args ...string) []Row {
	t.Helper()
	res := checks(e, proj, append([]string{"show", "--json"}, args...)...)
	if res.Code != 0 {
		t.Fatalf("show exited %d:\n%s", res.Code, res.Output)
	}
	var rows []Row
	if err := json.Unmarshal([]byte(res.Output), &rows); err != nil {
		t.Fatalf("show printed unreadable JSON: %v\n%s", err, res.Output)
	}
	return rows
}

const (
	judgeRule   = "match: \"docs/**\"\nchecks:\n  - judge: ./rubric.md.j2\n"
	rubric      = "Does this change to the docs hold up?\n{{ change }}\n"
	verdictPass = `{"pass": true, "reasoning": "fine now"}`
	verdictFail = `{"pass": false, "reasoning": "the ADR is not cited"}`
	promptFile  = ".git/judge-prompt"
)

// judged is a committed judged rule over docs/ and one commit after it; returns the rule's
// commit (the base of the range) with the judge shim answering verdict.
func judged(t *testing.T, e *Env, proj, verdict string) string {
	t.Helper()
	e.FileGuard(proj, "docs", judgeRule, map[string]string{"rubric.md.j2": rubric})
	base := e.CommitAll(proj, "the rule")
	e.WriteFile(proj, "docs/a.md", "the release is Friday\n")
	e.CommitAll(proj, "add a")
	e.InstallJudgeClaudeCapturing(proj, promptFile, verdict)
	return base
}

func contains(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Fatalf("output does not contain %q:\n%s", w, out)
		}
	}
}
