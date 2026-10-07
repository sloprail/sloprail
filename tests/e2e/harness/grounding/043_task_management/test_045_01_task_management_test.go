package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// task-management is a PreFileWrite gate (with a same-named file-guard for the Stop after-check) over `**/tasks/*/*/ASK.md`: every
// write must cite the user's own words (`require: [{citation: {source_types:
// [user]}}]`), and a prepare + judge then rules that the ask is TRUE to the cited
// words and holds THAT AND NOTHING ELSE. The citation rides on the write —
// `sr-file write …/ASK.md --cite:user '<quote>'` — never inside the file. Being a
// gate, a not-fine write is refused at PRE-tool, before it lands.
//
// The judge verdict is a stub (InstallJudgeClaude); the capturing variant records
// the rendered prompt so a test can see what the judge was handed.

const askPath = "memories/tasks/auth/001/ASK.md"

// authPrompt is the human message the session starts from — the citable words.
const authPrompt = "Please migrate the auth module to the new token format."

// writeAsk is the agent writing ASK.md with sr-file, citing quote.
func writeAsk(id, content, quote string) Turn {
	return Bash(id, "sr-file write "+askPath+" --cite:user "+shq(quote)+" --content "+shq(content))
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// T045_05: the cited words reach the judge's prompt, with where they sit in the
// record, and follow the session they came from — prepare -> template wiring.
func TestT045_05_CitedWordsReachTemplate(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": false, "reasoning": "x"}`)

	e.Run(proj, "s-045-05a", authPrompt, Turns("done",
		writeAsk("w1", authPrompt, "migrate the auth module to the new token format"),
	).ThenCommit("record the ask", harness.CitesUser("migrate the auth module to the new token format")))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran, so nothing about the wiring can be concluded")
	}
	if !containsStr(prompt, "migrate the auth module to the new token format") || !containsStr(prompt, ".jsonl:") {
		t.Errorf("the cited words and their location did not reach the template:\n%s", prompt)
	}

	other := "Add rate limiting to the public API gateway."
	e2 := New(t)
	proj2 := e2.Project()
	e2.GitInit(proj2)
	installExampleTree(t, proj2)
	e2.InstallJudgeClaudeCapturing(proj2, "judge-prompt.txt", `{"pass": false, "reasoning": "x"}`)

	e2.Run(proj2, "s-045-05b", other, Turns("done",
		writeAsk("w1", other, "rate limiting to the public API gateway"),
	).ThenCommit("record the ask", harness.CitesUser("rate limiting to the public API gateway")))

	prompt2 := e2.JudgePrompt(proj2, "judge-prompt.txt")
	if prompt2 == "" {
		t.Fatalf("the judge never ran for the second session")
	}
	if !containsStr(prompt2, "rate limiting to the public API gateway") {
		t.Errorf("the second session's cited words did not reach the template:\n%s", prompt2)
	}
	if containsStr(prompt2, "migrate the auth module") {
		t.Errorf("the template carried the previous session's words:\n%s", prompt2)
	}
}
