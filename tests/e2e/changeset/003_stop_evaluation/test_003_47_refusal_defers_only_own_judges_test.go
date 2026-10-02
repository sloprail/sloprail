package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_47: a cheap-check refusal in rule A defers only A's own judges. Rule B's
// judge still runs and its refusal surfaces in the same Stop. A's deferred judge
// is a visible skip row in `sr-checks verify`, not a silent absence.
func TestT003_47_ARefusalDefersOnlyItsOwnJudges(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project")
	e.FileGuard(proj, "a-script", "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n  - judge: ./rubric.md.j2\n",
		map[string]string{
			"check.sh":     "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"A-SCRIPT-REFUSED\"}'\nexit 1\n",
			"rubric.md.j2": "A-RUBRIC\n{{ change }}\n",
		})
	e.FileGuard(proj, "b-judge", judgeRule, map[string]string{"rubric.md.j2": rubric})
	e.CommitAll(proj, "the rules")
	e.InstallJudgeClaudeCapturing(proj, promptFile, verdictFail)

	e.Run(proj, "s-003-47", "write the docs", Turns("done", harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a")))
	joined := strings.Join(e.StopContinuations(proj, "s-003-47"), "\n")
	for _, want := range []string{"A-SCRIPT-REFUSED", "JUDGE-SAYS-NO"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the Stop's refusals lack %q (the other rule's judge must still run):\n%s", want, joined)
		}
	}
	if n := e.JudgeCalls(proj, promptFile, "A-RUBRIC"); n != 0 {
		t.Fatalf("rule A's own judge ran %d times though its script refused first", n)
	}
	status := e.CheckVerify(proj, "s-003-47", "origin/main", "HEAD").Output
	if !strings.Contains(status, "skip") || !strings.Contains(status, "judge deferred") {
		t.Fatalf("rule A's deferred judge is not shown as a skip with its reason:\n%s", status)
	}
}
