package e2e

// TODO(D3): drive verdict via a10n-claude-mock once a10n-cli#470 lands + new mock
// on PATH; today InstallJudgeClaude supplies the verdict.
//
// Use case: grounding-citations. A file-guard bound to `**/*.md`: every citation
// in a written doc must resolve (the cited source exists at the named range) and
// the quote must approximate the source. Two checks, cheap-gates-expensive:
//
//   - SCRIPT (citation-links-resolve.sh): every `[text](/abs/path:start-end)`
//     citation must resolve to a real file + range. Pure existence check — no
//     model. A citation to a missing file or a range that cannot be read refuses.
//   - PREPARE (fetch-cited-source.sh) + JUDGE (quote-approximates-source.md.j2):
//     prepare pulls the actual text at each cited range into additionalContext;
//     the judge rules whether each quote actually says what the source says.
//
// The guard is NOT preventive, so refusals arrive at Stop and are read with
// BlockingErrorsFrom(proj, sess, "Stop"); res.Refused() stays false.
//
// How each mechanism is driven:
//   - CITATIONS: written into the doc's own content as `[quote](/abs/path:a-b)`,
//     pointing at real source files placed on disk by e.WriteFile (cited by their
//     absolute path, the shape the script and prepare both parse).
//   - PREPARE/JUDGE: prepare fetches the cited lines; InstallJudgeClaude supplies
//     the model's pass/fail over quote-vs-source.
//
// The SCRIPT gives real, un-stubbed pass/fail: a citation that resolves vs one
// that does not is a genuinely different script input, independent of the judge
// stub — so the two failure axes are exercised separately.
//
// The example is installed VERBATIM — its scripts ship executable (mode 100755),
// so the rule's own logic runs as a user would get it (the script-refusal test
// carries an exec-bit regression tripwire that names it precisely if that regresses).

import (
	"path/filepath"
	"testing"
)

// gcProject stands up a project with the grounding-citations example installed
// VERBATIM (its scripts ship executable).
func gcProject(t *testing.T, e *env) string {
	t.Helper()
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, "grounding-citations")
	return proj
}

// cite renders a markdown citation `[quote](/abs/path:start-end)`.
func cite(quote, absPath string, start, end int) string {
	return "[" + quote + "](" + absPath + ":" +
		itoa(start) + "-" + itoa(end) + ")"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// T047_01: HAPPY PATH — the citation resolves to a real source range, and the
// judge rules the quote faithfully renders the source. The doc is admitted.
func TestT047_01_ResolvingCitationJudgePassesAdmits(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the quote matches the cited source line"}`)

	// A real source file the doc will cite by absolute path.
	e.WriteFile(proj, "src/data.txt", "alpha\nthe sky is blue on tuesdays\ngamma\n")
	absSrc := filepath.Join(proj, "src/data.txt")
	doc := "# Report\n\nAs recorded, " + cite("the sky is blue on tuesdays", absSrc, 2, 2) + ".\n"

	sess := "s-047-01"
	res := e.Run(proj, sess, "write a grounded report", Turns("done",
		Write("w1", "report.md", doc),
	))

	if res.Refused() {
		t.Fatalf("a resolving citation with a faithful quote was refused at pre-tool:\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("a resolving citation with a faithful quote blocked at Stop:\n%s", joinBlocks(blocks))
	}
}

// T047_02: SCRIPT REFUSAL — a citation that does NOT resolve. The cited file does
// not exist, so the link is dead before any quote-vs-source judgement. The SCRIPT
// refuses, and its reason (naming the unresolved reference) reaches the agent.
//
// The judge is stubbed to PASS: a block here can only be the script's.
func TestT047_02_UnresolvedCitationBlocksViaScript(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the script should refuse first"}`)

	absMissing := filepath.Join(proj, "src/nowhere.txt") // never created
	doc := "# Report\n\nAllegedly, " + cite("a claim", absMissing, 2, 2) + ".\n"

	sess := "s-047-02"
	e.Run(proj, sess, "write a report citing a missing source", Turns("done",
		Write("w1", "report.md", doc),
	))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a citation to a missing source was not refused by the script")
	}
	joined := joinBlocks(blocks)
	// Exec-bit regression tripwire: the example is installed verbatim, so if the
	// shipped citation-links-resolve.sh ever lost its 100755 mode the engine would
	// refuse it as unrunnable ("not executable") rather than running the citation
	// check. Naming it here reports the real cause instead of a confusing miss.
	if containsAll(joined, "not executable") {
		t.Fatalf("REGRESSION: the shipped citation-links-resolve.sh is not executable — the engine "+
			"refused it as unrunnable rather than running the citation check:\n%s", joined)
	}
	if !containsAll(joined, "do not resolve to a real source", "citations-resolve") {
		t.Fatalf("the unresolved-citation (script) reason did not reach the agent:\n%s", joined)
	}
}

// T047_03: JUDGE REFUSAL — the citation resolves (script passes), but the judge
// rules the quote does NOT approximate the source (a fabrication grounded in a
// real-but-wrong location). The block comes from the JUDGE and its reasoning
// reaches the agent.
//
// The source really says something else, so prepare feeds the judge a genuinely
// mismatched quote/source pair — not just the stub flipping.
func TestT047_03_ResolvingCitationJudgeFailBlocksViaJudge(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "the quote asserts a claim the cited line does not make"}`)

	e.WriteFile(proj, "src/data.txt", "alpha\nthe actual source says something unrelated\ngamma\n")
	absSrc := filepath.Join(proj, "src/data.txt")
	// The quote is a fabrication; it resolves to line 2, which says something else.
	doc := "# Report\n\nIt is claimed that " + cite("revenue tripled last quarter", absSrc, 2, 2) + ".\n"

	sess := "s-047-03"
	e.Run(proj, sess, "write a report with a fabricated quote", Turns("done",
		Write("w1", "report.md", doc),
	))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a resolving-but-mismatched citation was not refused by the judge")
	}
	joined := joinBlocks(blocks)
	if !containsAll(joined, "does not make", "citations-resolve") {
		t.Fatalf("the judge's reasoning did not reach the agent:\n%s", joined)
	}
}

// T047_04: DOES NOT FIRE OUTSIDE ITS MATCH — the guard is bound to `**/*.md`. A
// non-markdown file with a citation-shaped string that does NOT resolve is not
// selected at all, so nothing blocks even though the same string in a .md would
// be refused. This proves the match, and that the guard is not overreaching.
func TestT047_04_NonMarkdownDoesNotFire(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	// A verdict that must never be reached, since the guard should not match a .txt.
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "must not be reached — not a markdown file"}`)

	absMissing := filepath.Join(proj, "src/nowhere.txt")
	body := "notes: " + cite("a claim", absMissing, 2, 2) + "\n"

	sess := "s-047-04"
	res := e.Run(proj, sess, "write a non-markdown file with a dead citation", Turns("done",
		Write("w1", "notes.txt", body),
	))

	if res.Refused() {
		t.Fatalf("a non-markdown file was refused at pre-tool — the citation guard overreached:\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("a non-markdown file blocked — the `**/*.md` guard matched a .txt:\n%s", joinBlocks(blocks))
	}
}

// T047_05: NO CITATIONS AT ALL — a markdown doc with no citations passes the
// script (nothing to resolve) and the judge (no citations to mis-quote), and is
// admitted. The control that shows the guard admits clean work rather than
// blocking every .md — without it, a guard that refused everything would pass the
// refusal tests while being broken.
func TestT047_05_MarkdownWithoutCitationsAdmits(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "no citations to assess"}`)

	sess := "s-047-05"
	res := e.Run(proj, sess, "write a plain markdown doc", Turns("done",
		Write("w1", "notes.md", "# Just prose\n\nNothing is cited here at all.\n"),
	))

	if res.Refused() {
		t.Fatalf("a citation-free markdown doc was refused at pre-tool:\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("a citation-free markdown doc blocked — the guard refuses clean work:\n%s", joinBlocks(blocks))
	}
}
