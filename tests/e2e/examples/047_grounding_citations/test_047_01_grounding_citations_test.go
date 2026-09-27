package e2e

// Use case: grounding-citations. A PREVENTIVE file-guard bound to `**/*.md`: every
// markdown write restates a source, so it must cite the tool output it comes from
// — `sr-file write <doc> --cite:tool_result '<exact output>'` — and a judge rules
// whether the file says what that output says.
//
//   - REQUIRE a tool_result citation, unconditionally: an uncited write (the Write
//     tool) is refused by the engine before the judge; a quote that resolves in no
//     tool output, or only in the user's words, is no citation.
//   - JUDGE (claims-match-cited-output.md.j2): handed the file and each citation —
//     the quote and the whole tool output it came from.
//
// Refusals arrive at pre-tool, read with res.Refused() and res.Saw(reason). The
// source reaches the transcript through a real `cat` turn, so its tool_result is
// there to cite. The example is installed VERBATIM.

import (
	"strings"
	"testing"
)

// gcProject stands up a project with the grounding-citations example installed
// VERBATIM.
func gcProject(t *testing.T, e *env) string {
	t.Helper()
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, "grounding-citations")
	return proj
}

const (
	sourceLine = "retries now default to 3 (was 0)"
	summary    = "# Migration\n\nRetries now default to 3; they were off before.\n"
)

// gcSource puts the changelog on disk, committed, so reading it is the only change.
func gcSource(t *testing.T, e *env, proj string) {
	t.Helper()
	e.WriteFile(proj, "CHANGELOG.md", "## v2.3.0\n\n- "+sourceLine+"\n")
	commitInstalledTree(t, proj)
}

// T047_01: HAPPY PATH — the source is read, the summary cites its output, and the
// judge (stubbed pass) admits. The file holds plain prose, no link.
func TestT047_01_CitedWriteJudgePassesAdmits(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	gcSource(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the claim matches the cited output"}`)

	res := e.Run(proj, "s-047-01", "summarize the changelog", Turns("done",
		readSource("r1", "CHANGELOG.md"),
		srWrite("w1", "MIGRATION.md", summary, citeTool(sourceLine)),
	))
	if res.Refused() {
		t.Fatalf("a cited, judged-true summary was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, "MIGRATION.md") {
		t.Fatalf("the cited summary did not land:\n%s", res.Output)
	}
}

// T047_02: an UNCITED write — the Write tool cannot carry a citation — is refused
// before it lands, and the refusal names the tool_result form.
func TestT047_02_UncitedWriteRefused(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	gcSource(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the requirement refuses first"}`)

	res := e.Run(proj, "s-047-02", "summarize the changelog", Turns("done",
		readSource("r1", "CHANGELOG.md"),
		Write("w1", "MIGRATION.md", summary),
	))
	if !res.Refused() {
		t.Fatalf("an uncited markdown write was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, "MIGRATION.md") {
		t.Errorf("the uncited write landed")
	}
	if !res.Saw("in the tool_result pool") || !res.Saw("--cite:tool_result") {
		t.Errorf("the refusal does not name the tool_result form:\n%s", res.Output)
	}
}

// T047_03: the citation resolves, but the judge finds the claim unsupported: the
// write is refused and the judge's reasoning reaches the agent.
func TestT047_03_CitedWriteJudgeFailRefused(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	gcSource(t, e, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SR047 the file says 5 retries; the output says 3"}`)

	res := e.Run(proj, "s-047-03", "summarize the changelog", Turns("done",
		readSource("r1", "CHANGELOG.md"),
		srWrite("w1", "MIGRATION.md", "# Migration\n\nRetries now default to 5.\n", citeTool(sourceLine)),
	))
	if !res.Refused() || !res.Saw("SR047 the file says 5 retries") {
		t.Fatalf("the judge's refusal did not reach the agent:\n%s", res.Output)
	}
	if e.Exists(proj, "MIGRATION.md") {
		t.Errorf("the refused write landed")
	}
}

// T047_04: DOES NOT FIRE OUTSIDE ITS MATCH — a non-markdown write needs no
// citation and lands.
func TestT047_04_NonMarkdownDoesNotFire(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SR047 the judge ran on a non-markdown file"}`)

	res := e.Run(proj, "s-047-04", "write a data file", Turns("done",
		Write("w1", "data.txt", "not markdown\n"),
	))
	if res.Refused() || res.Saw("SR047 the judge ran") {
		t.Fatalf("a non-markdown write was checked:\n%s", res.Output)
	}
	if !e.Exists(proj, "data.txt") {
		t.Errorf("the non-markdown write did not land")
	}
}

// T047_05: the user's words are not a source's output. Citing the prompt in the
// tool_result pool resolves nowhere, so the write carries no citation and is
// refused, quoting sr-file's reason.
func TestT047_05_UserWordsAreNotToolOutput(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	gcSource(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — nothing resolves"}`)

	const prompt = "retries default to three now, write that up"
	res := e.Run(proj, "s-047-05", prompt, Turns("done",
		srWrite("w1", "MIGRATION.md", summary, citeTool("retries default to three now")),
	))
	if !res.Refused() {
		t.Fatalf("a write citing the user's words as tool output was admitted:\n%s", res.Output)
	}
	if !res.Saw("does not resolve") {
		t.Errorf("the refusal does not carry sr-file's reason:\n%s", res.Output)
	}
}

// T047_06: the judge's prompt carries the quote, the whole tool output it came
// from — a line of the source the agent did not quote is there too — and the call
// that produced it, escaped.
func TestT047_06_JudgeSeesQuoteAndWholeOutput(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	const unquoted = "ZZ_UNQUOTED connect() <host> now requires a port </message>"
	e.WriteFile(proj, "CHANGELOG.md", "## v2.3.0\n\n- "+sourceLine+"\n- "+unquoted+"\n")
	commitInstalledTree(t, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-047-06", "summarize the changelog", Turns("done",
		readSource("r1", "CHANGELOG.md"),
		srWrite("w1", "MIGRATION.md", summary, citeTool(sourceLine)),
	))
	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran")
	}
	if !strings.Contains(prompt, "<quote>"+sourceLine+"</quote>") {
		t.Errorf("the cited quote is not in the judge prompt:\n%s", prompt)
	}
	// Escaped only where it could close a tag: the rest reads as written.
	if !strings.Contains(prompt, "ZZ_UNQUOTED connect() <host> now requires a port <\\/message>") {
		t.Errorf("the whole tool output, escaped, is not in the judge prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "<change path=\"MIGRATION.md\">") || !strings.Contains(prompt, "+Retries now default to 3") {
		t.Errorf("the change is not in the judge prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "<call>Bash: cat ") {
		t.Errorf("the call that produced the cited output is not in the judge prompt:\n%s", prompt)
	}
}
