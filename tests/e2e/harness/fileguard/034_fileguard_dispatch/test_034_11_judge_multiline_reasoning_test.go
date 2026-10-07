package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// A judge's reasoning is often several lines (a numbered list of every gap). All of it reaches
// the stored verdict on sloprail/checks and the refusal the agent reads — not just the first
// line, which is what scraping the verifier's output line by line once kept.
func TestT034_11_JudgeMultilineReasoningReachesVerdictAndRefusal(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "substantive-memory", judgeGuard, map[string]string{"judge.md.j2": judgeGuardTemplate})
	e.CommitAll(proj, "the rule and its scripts")
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "1. alpha gap\n2. beta gap\n3. gamma gap"}`)

	e.Run(proj, "s-034-11", "write a thin memory", Turns("done",
		Write("w1", "memories/note.md", "meh"),
	).ThenCommit("add the memory"))

	joined := strings.Join(e.BlockingErrorsFrom(proj, "s-034-11", "Stop"), "\n")
	stored, err := json.Marshal(e.CacheRecords(proj))
	if err != nil {
		t.Fatalf("marshal the stored runs: %v", err)
	}
	for _, line := range []string{"1. alpha gap", "2. beta gap", "3. gamma gap"} {
		if !strings.Contains(joined, line) {
			t.Errorf("the refusal lost %q:\n%s", line, joined)
		}
		if !strings.Contains(string(stored), line) {
			t.Errorf("the stored verdict lost %q:\n%s", line, stored)
		}
	}
}
