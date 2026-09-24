package e2e

import (
	"path/filepath"
	"testing"
)

// T047_12: TWO citations in one doc, one resolving and one not — every existing
// test in this package cites exactly ONE source, so none proves
// citation-links-resolve.sh's own `while IFS= read -r ref` loop over ALL matched
// citations actually catches a failing one among several, rather than only ever
// being exercised with a single citation that either passes or fails outright.
//
// The judge is stubbed to PASS: a block here can only be the script's, and it
// must be attributable to the SPECIFIC unresolved citation, not a false refusal
// of the doc's other, perfectly good one.
func TestT047_12_OneOfTwoCitationsUnresolvedStillBlocks(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the script should refuse first"}`)

	e.WriteFile(proj, "src/data.txt", "alpha\nthe sky is blue on tuesdays\ngamma\n")
	goodSrc := filepath.Join(proj, "src/data.txt")
	absMissing := filepath.Join(proj, "src/nowhere.txt") // never created

	doc := "# Report\n\n" +
		"First, " + cite("the sky is blue on tuesdays", goodSrc, 2, 2) + ".\n\n" +
		"Second, " + cite("a claim", absMissing, 2, 2) + ".\n"

	sess := "s-047-12"
	e.Run(proj, sess, "write a report citing one good and one missing source", Turns("done",
		Write("w1", "report.md", doc),
	))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a doc with one resolving and one unresolved citation was not refused")
	}
	joined := joinBlocks(blocks)
	if !containsAll(joined, "do not resolve to a real source", "citations-resolve") {
		t.Fatalf("the unresolved-citation reason did not reach the agent:\n%s", joined)
	}
	// The reference itself (path:range) names the FAILING citation — present
	// only if the script's loop actually inspected both refs rather than
	// stopping at the first (good) one and never reaching the second.
	if !containsAll(joined, absMissing+":2-2") {
		t.Fatalf("the refusal did not name the specific unresolved citation (src/nowhere.txt:2-2) among the two cited — the loop may not have reached it:\n%s", joined)
	}
}

// T047_13: TWO citations, BOTH resolving, BOTH faithful — admits cleanly. The
// control for T047_12: without it, a script bug that refused on ANY doc with
// more than one citation (e.g. mishandling grep's multi-line output) would not
// be distinguished from correct multi-citation handling.
func TestT047_13_TwoResolvingCitationsBothAdmit(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "both quotes match their cited sources"}`)

	e.WriteFile(proj, "src/data.txt", "alpha\nthe sky is blue on tuesdays\ngamma\n")
	e.WriteFile(proj, "src/other.txt", "delta\nepsilon\nthe grass is green in spring\n")
	srcA := filepath.Join(proj, "src/data.txt")
	srcB := filepath.Join(proj, "src/other.txt")

	doc := "# Report\n\n" +
		"First, " + cite("the sky is blue on tuesdays", srcA, 2, 2) + ".\n\n" +
		"Second, " + cite("the grass is green in spring", srcB, 3, 3) + ".\n"

	sess := "s-047-13"
	res := e.Run(proj, sess, "write a report citing two good sources", Turns("done",
		Write("w1", "report.md", doc),
	))

	if res.Refused() {
		t.Fatalf("two resolving, faithful citations were refused at pre-tool:\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("two resolving, faithful citations blocked at Stop:\n%s", joinBlocks(blocks))
	}
}
