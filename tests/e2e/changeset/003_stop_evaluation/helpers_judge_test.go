package e2e

import "testing"

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
