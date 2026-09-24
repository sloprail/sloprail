package e2e

// T047_10/11: a citation range that is REVERSED or past-EOF, on a source file
// that genuinely exists. citation-links-resolve.sh's own exit-code-only check
// (`sed -n "${start},${end}p" "$file"`) was measured to admit both: sed exits 0
// on a reversed range too (it does not error — it prints the single addressed
// line, e.g. `sed -n "3,1p"` on an existing 3-line file prints line 3, not
// nothing), and exits 0 with EMPTY output on a past-EOF range. Neither is the
// "missing file" case T047_02 already covers. These are the cases the script's
// fix (validate start<=end before ever calling sed, then require sed's OUTPUT
// to be non-empty) exists for.

import (
	"path/filepath"
	"testing"
)

func TestT047_10_ReversedRangeCitationBlocksViaScript(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the script should refuse first"}`)

	source := filepath.Join(proj, "src/report.txt")
	e.WriteFile(proj, "src/report.txt", "line one\nline two\nline three\n")

	// start(3) > end(1): a reversed range on a file that genuinely exists.
	doc := "# Report\n\nAllegedly, " + cite("a claim", source, 3, 1) + ".\n"

	sess := "s-047-10"
	e.Run(proj, sess, "write a report with a backwards citation range", Turns("done",
		Write("w1", "report.md", doc),
	))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a citation with a REVERSED range (start > end) on an existing file was not refused — " +
			"the script's exit-code-only sed check was measured to admit this (sed prints the single " +
			"addressed line rather than erroring), so it must validate start<=end before calling sed")
	}
	joined := joinBlocks(blocks)
	if !containsAll(joined, "do not resolve to a real source", "citations-resolve") {
		t.Fatalf("the unresolved-citation (script) reason did not reach the agent:\n%s", joined)
	}
}

func TestT047_11_PastEOFRangeCitationBlocksViaScript(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the script should refuse first"}`)

	source := filepath.Join(proj, "src/report.txt")
	e.WriteFile(proj, "src/report.txt", "line one\nline two\nline three\n")

	// The file has 3 lines; the citation names 5-10, entirely past EOF.
	doc := "# Report\n\nAllegedly, " + cite("a claim", source, 5, 10) + ".\n"

	sess := "s-047-11"
	e.Run(proj, sess, "write a report citing lines past the end of the source", Turns("done",
		Write("w1", "report.md", doc),
	))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a citation whose range is entirely PAST EOF on an existing file was not refused — " +
			"the script's exit-code-only sed check was measured to admit this (sed exits 0 with empty " +
			"output), so it must require sed's own output to be non-empty, not just its exit code")
	}
	joined := joinBlocks(blocks)
	if !containsAll(joined, "do not resolve to a real source", "citations-resolve") {
		t.Fatalf("the unresolved-citation (script) reason did not reach the agent:\n%s", joined)
	}
}
