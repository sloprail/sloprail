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

// T045_01: a write that cites nothing is refused before any model is asked, and
// the refusal names the grounded way. The Write tool cannot carry a citation.
func TestT045_01_UncitedWriteRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-045-01", authPrompt, Turns("done",
		Write("w1", askPath, "Migrate the auth module to the new token format."),
	).ThenCommit("write the files"))

	if !res.Refused() {
		t.Fatalf("an uncited ASK.md write was not refused:\n%s", res.Output)
	}
	if !res.Saw("sr-file") || !res.Saw("--cite:user") {
		t.Errorf("the refusal does not say how to cite:\n%s", res.Output)
	}
	if e.Exists(proj, askPath) {
		t.Errorf("the uncited write landed")
	}
}

// T045_02: cited, so the cheap gate admits and the ask lands; but the judge (in the
// file-guard, at Stop) rejects the ask as padded beyond the cited words — the turn
// is blocked and the judge's reasoning reaches the agent.
func TestT045_02_CitedButRejectedByJudgeBlocksAtStop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "the ASK wraps the cited ask in agent-authored scope the human never asked for"}`)

	res := e.Run(proj, "s-045-02", authPrompt, Turns("done",
		writeAsk("w1", "Migrate the auth module — and also refactor logging, add metrics, and write docs.", "migrate the auth module"),
	).ThenCommit("record the ask", harness.CitesUser("migrate the auth module")))

	if res.Refused() {
		t.Fatalf("the gate (citation only, no model) refused a cited ask:\n%s", res.Output)
	}
	if !e.Exists(proj, askPath) {
		t.Errorf("the cited ask did not land: a gate holds no judge")
	}
	blocks := strings.Join(e.BlockingErrorsFrom(proj, "s-045-02", "Stop"), "\n")
	if !strings.Contains(blocks, "agent-authored scope the human never asked for") {
		t.Errorf("the judge's reasoning did not block the turn at Stop:\n%s", blocks)
	}
}

// T045_03: a quote the user never said grounds nothing — the write is refused and
// never lands.
func TestT045_03_UnresolvedQuoteBlocks(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-045-03", authPrompt, Turns("done",
		writeAsk("w1", "Migrate the auth module.", "the user never said this"),
	))

	if !res.Refused() {
		t.Fatalf("a write citing words the user never said was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, askPath) {
		t.Errorf("the write landed")
	}
}

// T045_04: a faithful, cited ask is admitted and lands — the happy path.
func TestT045_04_FaithfulAskAdmitsAndLands(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-045-04", authPrompt, Turns("done",
		writeAsk("w1", "Migrate the auth module to the new token format.\n", "migrate the auth module to the new token format"),
	))

	if res.Refused() {
		t.Fatalf("a faithful, cited ask was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, askPath) {
		t.Fatalf("the admitted write did not land")
	}
}

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

// T045_06: a write the match does not select is never judged — the does-not-fire
// control. A RESULT.md in the same task folder, and an ordinary file, are left
// alone even uncited and with the judge stubbed to FAIL.
func TestT045_06_NonAskWritesAreNeverJudged(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "would block if it fired"}`)

	res := e.Run(proj, "s-045-06", "implement the migration", Turns("done",
		Write("w1", "memories/tasks/auth/001/RESULT.md", "Implemented the token migration."),
		Write("w2", "memories/notes/scratch.md", "a scratch note"),
	).ThenCommit("write the files"))

	if res.Refused() {
		t.Fatalf("a write outside ASK.md was refused — the guard fired where it must not:\n%s", res.Output)
	}
	if !e.Exists(proj, "memories/tasks/auth/001/RESULT.md") || !e.Exists(proj, "memories/notes/scratch.md") {
		t.Errorf("an unguarded write did not land")
	}
}

// T045_07: editing an existing ask without a citation is refused — the rewrite the
// rule exists to stop (the ask edited down to match the work) cannot happen
// silently.
func TestT045_07_UncitedEditOfAskRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, askPath, "Migrate the auth module to the new token format.\n")
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-045-07", "the migration is half done", Turns("done",
		Write("w1", askPath, "Migrate part of the auth module.\n"),
	).ThenCommit("write the files"))

	if !res.Refused() {
		t.Fatalf("an uncited rewrite of the ask was not refused:\n%s", res.Output)
	}
	body, _ := readProj(proj, askPath)
	if body != "Migrate the auth module to the new token format.\n" {
		t.Errorf("the ask changed despite the refusal: %q", body)
	}
}

// T045_08: the gate's citation rides the write, the file-guard's rides the commit. A
// write cited to the gate but committed with no `Sloprail-Cites-User` trailer is
// refused at Stop for the missing citation (before any judge), and the same ask
// committed with the trailer passes.
func TestT045_08_CommitMustCiteTheUsersWords(t *testing.T) {
	e := NewUncited(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	ask := writeAsk("w1", "Migrate the auth module to the new token format.\n", "migrate the auth module to the new token format")
	e.Run(proj, "s-045-08", authPrompt, Turns("done", ask).ThenCommit("record the ask"))

	blocks := strings.Join(e.BlockingErrorsFrom(proj, "s-045-08", "Stop"), "\n")
	if !strings.Contains(blocks, "must cite the user's own words") {
		t.Fatalf("an uncited commit of ASK.md was not refused at Stop for its citation:\n%s", blocks)
	}

	seen := len(e.AllBlockingErrorsFrom(proj, "s-045-08", "Stop"))
	if seen == 0 {
		t.Fatalf("the refusal did not hold the turn")
	}
	// A citation grounds the files its own commit changed, so it is added by amending
	// the commit that changed the ask.
	e.Run(proj, "s-045-08", "go on", Turns("done", harness.AmendLast("amend", "record the ask",
		harness.CitesUser("migrate the auth module to the new token format"))))
	if got := len(e.AllBlockingErrorsFrom(proj, "s-045-08", "Stop")); got != seen {
		t.Fatalf("a commit citing the user's words was still refused (%d refusals, had %d):\n%s", got, seen,
			strings.Join(e.BlockingErrorsFrom(proj, "s-045-08", "Stop"), "\n"))
	}
}
