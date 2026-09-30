package e2e

import (
	"testing"
)

// This file covers a file-guard's JUDGE check: the Runner renders the guard's
// template against the Changeset payload (changeset.files[], plus
// additionalContext when a prepare ran), sr-agent asks the
// model (a fixed stub here), and the verdict blocks or admits at Stop. It is the
// whole judge path with only the model's answer replaced by a fixed one.

// judgeGuard is a file-guard whose check is a judge over memories/ files: the
// model judges whether the note is substantive. It reads changeset.files[].newContent — the
// committed content — off the Changeset.
const judgeGuard = `match: memories/**/*.md
checks:
  - judge: ./judge.md.j2
`

// judgeGuardTemplate renders against the Changeset the judge is handed:
// changeset.files[].newContent is each committed file's content. Proving the
// file-guard judge input carries the committed state where the spec says it does.
const judgeGuardTemplate = `# Judge whether this memory is substantive

The file's content:

{% for f in changeset.files %}{{ f.newContent }}
{% endfor %}

A memory that is one word or empty is not substantive and must fail.
`

// T034_09: a file-guard judge check that returns a FAILING verdict blocks the
// turn, having rendered the file's content into its prompt.
func TestT034_09_JudgeGuardBlocks(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "substantive-memory", judgeGuard, map[string]string{"judge.md.j2": judgeGuardTemplate})
	e.DisableShippedFileGuards(proj)
	e.CommitAll(proj, "the rule and its scripts")
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "this memory is one word, not substantive"}`)

	e.Run(proj, "s-034-09", "write a thin memory", Turns("done",
		Write("w1", "memories/note.md", "meh"),
	).ThenCommit("add the memory"))

	blocks := e.BlockingErrorsFrom(proj, "s-034-09", "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a file-guard judge check returning a failing verdict did not block the turn")
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	if !containsStr(joined, "not substantive") {
		t.Errorf("the judge's reasoning did not reach the agent:\n%s", joined)
	}
}

// T034_10: a file-guard judge check that returns a PASSING verdict admits — the
// turn ends. The control for T034_09.
func TestT034_10_JudgeGuardAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "substantive-memory", judgeGuard, map[string]string{"judge.md.j2": judgeGuardTemplate})
	e.DisableShippedFileGuards(proj)
	e.CommitAll(proj, "the rule and its scripts")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-034-10", "write a real memory", Turns("done",
		Write("w1", "memories/note.md", "A thorough note about the decision and its rationale."),
	).ThenCommit("add the memory"))

	if len(e.BlockingErrorsFrom(proj, "s-034-10", "Stop")) != 0 {
		t.Errorf("a file-guard judge check returning a passing verdict blocked the turn anyway")
	}
}
