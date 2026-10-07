package e2e

// Use case: grounding-citations. A GATE on PreFileWrite of `*.md`, and a plain
// file-guard of the same name on `**/*.md` for the Stop after-check: every
// markdown write restates a source, so it must cite the tool output it comes from
// — `sr-file write <doc> --cite:tool_result '<exact output>'` — and a judge rules
// whether the file says what that output says.
//
//   - REQUIRE a tool_result citation, unconditionally: an uncited write (the Write
//     tool) is refused by the engine before the judge; a quote that resolves in no
//     tool output, or only in the user's words, is no citation.
//   - JUDGE (claims-match-cited-output.md.j2): handed the file and each citation —
//     the quote and the whole tool output it came from.
//   - FAIL CLOSED (the gate's require-known-result.sh): a write whose result the
//     engine could not work out ahead is refused before the judge reads no bytes.
//
// The gate's refusals arrive at pre-tool, read with res.Refused() and
// res.Saw(reason); the file-guard's arrive at Stop. The
// source reaches the transcript through a real `cat` turn, so its tool_result is
// there to cite. The example is installed VERBATIM.

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"strings"
	"testing"
)

// gcProjectWith is gcProject with the changelog's text given.
func gcProjectWith(t *testing.T, e *env, changelog string) string {
	t.Helper()
	proj := e.Project()
	e.GitInit(proj)
	// The changelog the summaries restate is committed BEFORE the rule is: it is the
	// baseline, and reading it is the only change. In the rule's own range a markdown
	// file would itself have to cite a tool's output, per file.
	e.WriteFile(proj, "CHANGELOG.md", changelog)
	e.CommitAll(proj, "the source changelog")
	installExampleTree(t, proj, "grounding-citations")
	return proj
}

const (
	sourceLine = "retries now default to 3 (was 0)"
	summary    = "# Migration\n\nRetries now default to 3; they were off before.\n"
)

// T047_06: the judge's prompt carries the quote, the whole tool output it came
// from — a line of the source the agent did not quote is there too — and the call
// that produced it, escaped.
func TestT047_06_JudgeSeesQuoteAndWholeOutput(t *testing.T) {
	e := newEnv(t)
	const unquoted = "ZZ_UNQUOTED connect() <host> now requires a port </message>"
	proj := gcProjectWith(t, e, "## v2.3.0\n\n- "+sourceLine+"\n- "+unquoted+"\n")
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-047-06", "summarize the changelog", Turns("done",
		readSource("r1", "CHANGELOG.md"),
		srWrite("w1", "MIGRATION.md", summary, citeTool(sourceLine)),
	).ThenCommit("write the files", harness.CitesTool(sourceLine)))
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
	if !strings.Contains(prompt, "+++ b/MIGRATION.md") || !strings.Contains(prompt, "+Retries now default to 3") {
		t.Errorf("the change is not in the judge prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "<call>Bash: cat ") {
		t.Errorf("the call that produced the cited output is not in the judge prompt:\n%s", prompt)
	}
}
