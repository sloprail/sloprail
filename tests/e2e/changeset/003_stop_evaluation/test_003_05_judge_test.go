package e2e

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"strings"
	"testing"
)

// A judge rule: the rubric renders {{ change }} (the combined diff of the
// selected files) and {{ changeset }} (the payload). The model is stood in for by
// the claude shim, which records the rendered prompt and every call.

const judgeRule = "match: \"docs/**\"\nchecks:\n  - judge: ./rubric.md.j2\n"

const rubric = `JUDGE-RUBRIC
Does this change to the docs hold up?
HEAD={{ changeset.head }} BASE={{ changeset.base }} SUBJECT={{ subject.id }}
FILES:{% for f in changeset.files %} {{ f.path }}({{ f.status }}){% endfor %}
COMMITS:{% for c in changeset.commits %} [{{ c.subject }}]{% endfor %}
CHANGE:
{{ change }}
`

const (
	verdictFail = `{"pass": false, "reasoning": "JUDGE-SAYS-NO: the doc contradicts itself"}`
	verdictPass = `{"pass": true, "reasoning": "fine"}`
	promptFile  = ".git/judge-prompt"
)

func judgeProject(t *testing.T, verdict string) (*Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project")
	e.FileGuard(proj, "docs", judgeRule, map[string]string{"rubric.md.j2": rubric})
	e.CommitAll(proj, "the judged rule")
	e.InstallJudgeClaudeCapturing(proj, promptFile, verdict)
	return e, proj
}

// T003_05: the judge is handed the changeset: {{ change }} is the combined diff
// of the selected files, {{ changeset }} the payload, {{ subject }} the unit
// judged. Its refusal reaches the agent.
func TestT003_05_TheJudgeRendersTheChangeset(t *testing.T) {
	e, proj := judgeProject(t, verdictFail)

	e.Run(proj, "s-003-05", "write the docs", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a"),
		harness.CommitFile("c2", "docs/b.md", "the release is Monday", "add b"),
	))

	joined := strings.Join(e.BlockingErrorsFrom(proj, "s-003-05", "Stop"), "\n")
	if !strings.Contains(joined, "JUDGE-SAYS-NO") {
		t.Fatalf("the judge's refusal did not reach the agent:\n%s", joined)
	}
	prompt := e.JudgePrompt(proj, promptFile)
	for _, want := range []string{
		"JUDGE-RUBRIC",
		"SUBJECT=changeset",
		"docs/a.md(A)", "docs/b.md(A)",
		"[add a]", "[add b]",
		"+++ b/docs/a.md", "+the release is Friday",
		"+++ b/docs/b.md", "+the release is Monday",
		"HEAD=" + e.Git(proj, "rev-parse", "HEAD"),
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the rendered prompt lacks %q:\n%s", want, prompt)
		}
	}
}

// T003_06: a failing judge is TERMINAL. It is asked once; every later Stop over
// the same input replays its verdict from the store without asking the model. A
// fix changes the input, so the rule is judged again over the whole squashed
// range and can pass — and the old failure is then stale (skip), not outstanding.
func TestT003_06_AFailIsReplayedUntilTheInputChanges(t *testing.T) {
	e, proj := judgeProject(t, verdictFail)

	e.Run(proj, "s-003-06", "write the doc", Turns("done", harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a")))
	if n := len(e.StopContinuations(proj, "s-003-06")); n < 1 {
		t.Fatal("premise: the refusal should have driven the agent on through further Stops")
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != 1 {
		t.Fatalf("the judge was asked %d times across the refused Stops; a fail is terminal and must be replayed, not re-judged", n)
	}

	// Another Stop on exactly the same input: still refused, still one call.
	r := e.StopNow(proj, "s-003-06", false)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "JUDGE-SAYS-NO") {
		t.Fatalf("the replayed failure was not refused with the judge's reasoning:\n%s", r.Output)
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != 1 {
		t.Fatalf("a Stop over unchanged input asked the judge again (%d calls)", n)
	}
	if out := e.ChecksStatus(proj, "s-003-06", "--failing"); !strings.Contains(out, "fail") || !strings.Contains(out, "JUDGE-SAYS-NO") {
		t.Fatalf("the failure is not outstanding in the check results:\n%s", out)
	}

	// A fix changes the input: judged again — over the whole squashed range — and
	// passes.
	e.InstallJudgeClaudeCapturing(proj, promptFile, verdictPass)
	e.Run(proj, "s-003-06", "fix it", Turns("fixed", harness.CommitFile("c2", "docs/a.md", "the release is Monday", "fix a")))
	if n := e.JudgeCalls(proj, promptFile, ""); n != 2 {
		t.Fatalf("the fix was not judged (%d calls)", n)
	}
	prompt := e.JudgePrompt(proj, promptFile)
	if !strings.Contains(prompt, "[add a]") || !strings.Contains(prompt, "[fix a]") {
		t.Fatalf("the fix was judged without the original commit in the range:\n%s", prompt)
	}

	// The old failure is cleared as stale, not left standing forever.
	if out := e.ChecksStatus(proj, "s-003-06", "--failing"); strings.TrimSpace(out) != "" {
		t.Fatalf("a stale failure is still outstanding:\n%s", out)
	}
	sql := e.ChecksSQL(proj, "s-003-06", "select status, json_extract(metadata, '$.reason') as reason from checks where json_extract(metadata, '$.reasoning') like '%JUDGE-SAYS-NO%'")
	if !strings.Contains(sql.Output, `"skip"`) || !strings.Contains(sql.Output, "stale") {
		t.Fatalf("the superseded failure should be a stale skip:\n%s", sql.Output)
	}

	// And it passed at a head: the watermark moved, nothing new is judged.
	e.Run(proj, "s-003-06", "done?", Turns("yes", Bash("b1", "true")))
	if n := e.JudgeCalls(proj, promptFile, ""); n != 2 {
		t.Fatalf("a Stop with nothing new asked the judge (%d calls)", n)
	}
}
