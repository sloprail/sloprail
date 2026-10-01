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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gcProject stands up a project with the grounding-citations example installed
// VERBATIM.
func gcProject(t *testing.T, e *env) string {
	t.Helper()
	return gcProjectWith(t, e, "## v2.3.0\n\n- "+sourceLine+"\n")
}

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

// T047_01: HAPPY PATH — the source is read, the summary cites its output, and the
// judge (stubbed pass) admits. The file holds plain prose, no link.
func TestT047_01_CitedWriteJudgePassesAdmits(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the claim matches the cited output"}`)

	res := e.Run(proj, "s-047-01", "summarize the changelog", Turns("done",
		readSource("r1", "CHANGELOG.md"),
		srWrite("w1", "MIGRATION.md", summary, citeTool(sourceLine)),
	).ThenCommit("write the files", harness.CitesTool(sourceLine)))
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
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the requirement refuses first"}`)

	res := e.Run(proj, "s-047-02", "summarize the changelog", Turns("done",
		readSource("r1", "CHANGELOG.md"),
		Write("w1", "MIGRATION.md", summary),
	).ThenCommit("write the files"))
	if !res.Refused() {
		t.Fatalf("an uncited markdown write was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, "MIGRATION.md") {
		t.Errorf("the uncited write landed")
	}
	if !res.Saw("a tool's output from this session") || !res.Saw("--cite:tool_result") {
		t.Errorf("the refusal does not name the tool_result form:\n%s", res.Output)
	}
}

// T047_03: the citation resolves, so the cheap gate admits and the write lands; but
// the judge (in the file-guard, at Stop) finds the claim unsupported: the turn is
// blocked and the judge's reasoning reaches the agent. Then, corrected to what the
// source says, the same session passes.
func TestT047_03_CitedWriteJudgeFailBlocksAtStop(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SR047 the file says 5 retries; the output says 3"}`)

	res := e.Run(proj, "s-047-03", "summarize the changelog", Turns("done",
		readSource("r1", "CHANGELOG.md"),
		srWrite("w1", "MIGRATION.md", "# Migration\n\nRetries now default to 5.\n", citeTool(sourceLine)),
	).ThenCommit("write the files", harness.CitesTool(sourceLine)))
	if res.Refused() {
		t.Fatalf("the gate (citation only, no model) refused a cited write:\n%s", res.Output)
	}
	if !e.Exists(proj, "MIGRATION.md") {
		t.Errorf("the cited write did not land: a gate holds no judge")
	}
	blocks := strings.Join(e.BlockingErrorsFrom(proj, "s-047-03", "Stop"), "\n")
	if !strings.Contains(blocks, "SR047 the file says 5 retries") {
		t.Fatalf("the judge's reasoning did not block the turn at Stop:\n%s", blocks)
	}
	if !strings.Contains(blocks, "citations-resolve") {
		t.Errorf("the block does not name the file-guard:\n%s", blocks)
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
	).ThenCommit("write the files"))
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
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — nothing resolves"}`)

	const prompt = "retries default to three now, write that up"
	res := e.Run(proj, "s-047-05", prompt, Turns("done",
		srWrite("w1", "MIGRATION.md", summary, citeTool("retries default to three now")),
	).ThenCommit("write the files"))
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

// T047_07: a call that writes several markdown files is asked about EVERY one, and
// one refusal names the file that is not grounded — the call runs for none.
func TestT047_07_OneUngroundedFileRefusesTheWholeCall(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the requirement refuses the uncited file"}`)

	res := e.Run(proj, "s-047-07", "summarize the changelog twice", Turns("done",
		readSource("r1", "CHANGELOG.md"),
		Bash("w1", "sr-file write A.md --content "+shq(summary)+" "+citeTool(sourceLine)+
			" && sr-file write B.md --content "+shq(summary)),
	).ThenCommit("write the files"))
	if !res.Refused() || !res.Saw("B.md") {
		t.Fatalf("a call with one uncited markdown file was not refused, naming it:\n%s", res.Output)
	}
	if e.Exists(proj, "A.md") || e.Exists(proj, "B.md") {
		t.Errorf("a refused call still wrote a file (A.md: %v, B.md: %v)", e.Exists(proj, "A.md"), e.Exists(proj, "B.md"))
	}
}

// T047_08: an uncited redirect (`>`), a result the engine cannot compute, carries
// no citation either and is refused before it lands — the gate does not admit
// bytes nobody saw.
func TestT047_08_ShellRedirectIsRefused(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the requirement refuses first"}`)

	res := e.Run(proj, "s-047-08", "summarize the changelog", Turns("done",
		readSource("r1", "CHANGELOG.md"),
		Bash("w1", "printf 'Retries now default to 3.\\n' > MIGRATION.md"),
	).ThenCommit("write the files"))
	if !res.Refused() || !res.Saw(`gate \"citations-resolve\"`) {
		t.Fatalf("an uncited shell write of markdown was not refused by the gate:\n%s", res.Output)
	}
	if e.Exists(proj, "MIGRATION.md") {
		t.Errorf("the refused redirect landed")
	}
}

// T047_10: the Stop after-check. A script rewriting markdown is not a write the
// engine sees ahead, so the gate never asks; the file-guard of the same name asks
// at Stop, and the change carries no citation, so it blocks the turn.
func TestT047_10_ScriptRewriteIsCaughtAtStop(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the requirement refuses first"}`)

	sess := "s-047-10"
	e.Run(proj, sess, "summarize the changelog", Turns("done",
		readSource("r1", "CHANGELOG.md"),
		Bash("w1", `python3 -c "open('MIGRATION.md','w').write('Retries now default to 3.\\n')"`),
	).ThenCommit("write the files"))
	if !e.Exists(proj, "MIGRATION.md") {
		t.Fatalf("the script rewrite did not land, so this no longer tests the Stop after-check")
	}
	joined := joinBlocks(e.BlockingErrorsFrom(proj, sess, "Stop"))
	if !containsAll(joined, "citations-resolve", "MIGRATION.md", "must cite a tool's output") {
		t.Fatalf("an uncited script-written markdown file was not refused at Stop:\n%s", joined)
	}
}

// T047_11: the gate's require-known-result.sh refuses a create or update whose
// result the engine could not work out, and admits one whose result is known.
func TestT047_11_UnknownResultIsRefused(t *testing.T) {
	script := filepath.Join(repoRoot(t), "examples", "grounding-citations", ".sloprail", "gate", "citations-resolve", "require-known-result.sh")
	for _, c := range []struct {
		known string
		want  int
	}{{"false", 1}, {"true", 0}} {
		cmd := exec.Command(script)
		cmd.Stdin = strings.NewReader(`{"event":{"kind":"PreFileUpdate","path":"NOTES.md","resultKnown":` + c.known + `,"newContent":""}}`)
		out, _ := cmd.Output()
		code := cmd.ProcessState.ExitCode()
		if code != c.want {
			t.Errorf("resultKnown %s: exit %d, want %d (%s)", c.known, code, c.want, out)
		}
		if c.want == 1 && !strings.Contains(string(out), "cannot be worked out before it runs") {
			t.Errorf("the refusal does not say why: %s", out)
		}
	}
}

// T047_12: the gate's citation rides the write, the file-guard's rides the commit. A
// write the gate admitted (cited to it) but committed with no `Sloprail-Cites-Tool`
// trailer is refused at Stop for the missing citation, before any judge; amending
// the commit that changed the files to cite the source output passes.
func TestT047_12_CommitMustCiteTheSource(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the claim matches the cited output"}`)

	const sess = "s-047-12"
	e.Run(proj, sess, "summarize the changelog", Turns("done",
		readSource("r1", "CHANGELOG.md"),
		srWrite("w1", "MIGRATION.md", summary, citeTool(sourceLine)),
	).ThenCommit("write the summary"))
	blocks := joinBlocks(e.BlockingErrorsFrom(proj, sess, "Stop"))
	if !containsAll(blocks, "citations-resolve", "must cite a tool's output") {
		t.Fatalf("an uncited commit of markdown was not refused at Stop:\n%s", blocks)
	}
	seen := len(e.StopContinuations(proj, sess))

	// A citation grounds the files its own commit changed, so it is added by amending
	// the commit that changed them.
	e.Run(proj, sess, "cite it", Turns("done", harness.AmendLast("amend", "write the summary", harness.CitesTool(sourceLine))))
	if got := len(e.StopContinuations(proj, sess)); got != seen {
		t.Fatalf("a commit citing the source output was still refused (%d refusals, had %d):\n%s", got, seen,
			joinBlocks(e.BlockingErrorsFrom(proj, sess, "Stop")))
	}
}
