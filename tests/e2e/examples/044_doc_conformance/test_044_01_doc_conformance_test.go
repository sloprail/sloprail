package e2e

import (
	"testing"
)

// TODO(D3): drive the verdict via a10n-claude-mock once a10n-cli#470's mock grows
// sr-agent's claude-flag surface for this path; today the proven InstallJudgeClaude
// stub supplies the model verdict (the same substitution T034_09/10 make).
//
// doc-conformance is a file-guard matched by an `sr:docs` marker whose fqn is a
// remote doc URL — mirroring the a10n-cli convention of citing a doc section as
// `a10n:docs <URL>` in a comment, spelled as an sr: marker so sloprail's own
// tooling can bind a rule to it. Its one check is a judge (no prepare, no script
// tier): the model visits the URL and rules on whether the marked code conforms.
// The marker is what SELECTS the file; the template renders the marker's URL and
// the marked file's content into the prompt.
//
// The scenarios prove: a non-conforming marked file BLOCKS at Stop and the judge's
// reasoning reaches the agent (violation); a conforming marked file ADMITS (happy);
// a file WITHOUT the marker — or carrying a marker of a DIFFERENT kind — is never
// judged (the match control, since here the match is a marker not a path); the
// marker's URL and the file's content reach the rendered template (the wiring, this
// example's stand-in for prepare -> template since it has no prepare), changing
// with the marker; and the file-guard RE-FIRE until the file is fixed.

const docURL = "https://docs.example.invalid/claude-code/trajectory-jsonl"
const otherDocURL = "https://docs.example.invalid/claude-code/hooks"

// markedMock is a mock-emulator file carrying the docs marker. The `//` leader
// and a bare-URL fqn are a well-formed marker (kind `docs`), so the scan selects
// this file. Its body stands in for code that claims to follow the doc at that
// URL.
func markedMock(url, body string) string {
	return "// sr:docs " + url + "\npackage mock\n\n" + body + "\n"
}

// T044_01: a marked file whose code the judge finds non-conforming BLOCKS at Stop,
// and the judge's reasoning reaches the agent.
//
// The marker selects the file; the judge (stub pass:false) refuses with the
// reasoning the rule would give; the guard blocks at Stop and the words reach the
// agent.
func TestT044_01_NonConformingMarkedFileBlocks(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "the mock emits a toolUseResult field the linked doc never describes"}`)

	e.Run(proj, "s-044-01", "write a mock that claims to follow the doc", Turns("done",
		Write("w1", "internal/mock/trajectory.go", markedMock(docURL, "func Emit() string { return `{\"toolUseResult\":{}}` }")),
	))

	blocks := e.BlockingErrorsFrom(proj, "s-044-01", "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a marked file the judge found non-conforming did not block at Stop")
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	if !containsStr(joined, "doc never describes") {
		t.Errorf("the judge's reasoning did not reach the agent:\n%s", joined)
	}
}

// T044_02: a conforming marked file ADMITS — the pass control for T044_01.
//
// Same marker, same rule, opposite verdict; the only difference is the stub's
// answer, standing in for the model finding the code DOES conform. Without this a
// guard that blocked every marked file would pass T044_01 while being broken.
func TestT044_02_ConformingMarkedFileAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-044-02", "write a mock that follows the doc", Turns("done",
		Write("w1", "internal/mock/trajectory.go", markedMock(docURL, "func Emit() string { return `{\"type\":\"assistant\"}` }")),
	))

	if blocks := e.BlockingErrorsFrom(proj, "s-044-02", "Stop"); len(blocks) != 0 {
		t.Errorf("a conforming marked file was blocked anyway:\n%v", blocks)
	}
}

// T044_03: a file WITHOUT the docs marker — or carrying a marker of a DIFFERENT
// kind — is never judged.
//
// The match control, and the one that matters most: the judge is stubbed to FAIL,
// so any firing would block. A plain unmarked file is left alone; a file carrying
// an `sr:invariant` marker (a real marker, wrong KIND) is left alone too — proving
// the match keys on the marker's kind, not merely on the presence of any marker.
func TestT044_03_UnmarkedOrWrongKindIsNeverJudged(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "would block if it fired"}`)

	e.Run(proj, "s-044-03", "write unmarked and wrong-kind files", Turns("done",
		// No marker at all.
		Write("w1", "internal/mock/plain.go", "package mock\n\nfunc Emit() string { return \"{}\" }\n"),
		// A well-formed marker of a DIFFERENT kind — must not match docs.
		Write("w2", "internal/mock/other.go", "// sr:invariant Mock.id\npackage mock\n"),
	))

	if blocks := e.BlockingErrorsFrom(proj, "s-044-03", "Stop"); len(blocks) != 0 {
		t.Errorf("the guard fired on a file with no docs marker:\n%v", blocks)
	}
	if !e.Exists(proj, "internal/mock/plain.go") || !e.Exists(proj, "internal/mock/other.go") {
		t.Errorf("the unguarded writes did not land at all")
	}
}

// T044_04: the marker's URL and the file's content reach the rendered template —
// the wiring, proven directly and shown to change with the marker.
//
// The template renders {{ m.fqn }} (the marker's URL, the thing the agent would
// visit) and {{ event.newContent }}. A stubbed verdict cannot show either reached
// the template (undefined renders empty), so the capturing shim records the prompt
// and the test asserts the marker's OWN URL and the file's body appear in it — and
// that a DIFFERENT URL in a fresh session renders a DIFFERENT prompt.
func TestT044_04_MarkerURLReachesTemplate(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": false, "reasoning": "x"}`)

	e.Run(proj, "s-044-04a", "write a marked mock", Turns("done",
		Write("w1", "internal/mock/trajectory.go", markedMock(docURL, "func Emit() string { return marker_body_alpha() }")),
	))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran, so nothing about the wiring can be concluded")
	}
	// The marker's URL — the thing the judge is told to visit — reached the prompt.
	if !containsStr(prompt, docURL) {
		t.Errorf("the marker's doc URL (m.fqn) did not reach the template:\n%s", prompt)
	}
	// The marked file's own content reached the prompt.
	if !containsStr(prompt, "marker_body_alpha") {
		t.Errorf("event.newContent did not reach the template:\n%s", prompt)
	}

	// A DIFFERENT marker URL and body, fresh session: the prompt must follow it.
	e2 := New(t)
	proj2 := e2.Project()
	e2.GitInit(proj2)
	installExampleTree(t, proj2)
	e2.InstallJudgeClaudeCapturing(proj2, "judge-prompt.txt", `{"pass": false, "reasoning": "x"}`)

	e2.Run(proj2, "s-044-04b", "write a differently-marked mock", Turns("done",
		Write("w1", "internal/mock/hooks.go", markedMock(otherDocURL, "func Fire() string { return marker_body_beta() }")),
	))

	prompt2 := e2.JudgePrompt(proj2, "judge-prompt.txt")
	if prompt2 == "" {
		t.Fatalf("the judge never ran for the second marker")
	}
	if !containsStr(prompt2, otherDocURL) || !containsStr(prompt2, "marker_body_beta") {
		t.Errorf("the second marker's URL/content did not reach the template:\n%s", prompt2)
	}
	if containsStr(prompt2, docURL) || containsStr(prompt2, "marker_body_alpha") {
		t.Errorf("the template carried a previous run's marker — the render is not following the file:\n%s", prompt2)
	}
}

// T044_05: a marked non-conforming file RE-FIRES each cycle until it is FIXED.
//
// Same four-cycle shape as the content-de-layering re-fire, with per-cycle distinct
// reasons so the shared transcript can tell them apart:
//   - Cycle 1 writes the marked non-conforming file; the judge refuses (R1).
//   - Cycle 2 writes an UNMARKED file (nothing the guard matches); a block here can
//     only be the re-judged outstanding marked file (R2) — proving the re-fire.
//   - Cycle 3 FIXES the marked file (rewrites it to conforming); the judge passes.
//   - Cycle 4 writes another unmarked file with the judge armed to refuse (R4); R4
//     must NOT appear, proving the fixed file cleared and stopped re-firing.
func TestT044_05_NonConformingReFiresUntilFixed(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	sess := "s-044-05"

	e.InstallJudgeClaude(`{"pass": false, "reasoning": "R1 mock diverges from the linked doc"}`)
	e.Run(proj, sess, "write a non-conforming marked mock", Turns("done",
		Write("w1", "internal/mock/trajectory.go", markedMock(docURL, "func Emit() string { return `{\"toolUseResult\":{}}` }")),
	))
	if !hasReason(e, proj, sess, "R1") {
		t.Fatalf("the non-conforming marked file did not block in the first cycle")
	}

	// Cycle 2: an UNMARKED file — the guard matches nothing here, so any block is
	// the re-fired outstanding marked file.
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "R2 the marked mock is still outstanding"}`)
	e.Run(proj, sess, "write an unmarked helper", Turns("done",
		Write("w2", "internal/mock/helper.go", "package mock\n\nfunc Helper() {}\n"),
	))
	if !hasReason(e, proj, sess, "R2") {
		t.Fatalf("the outstanding marked file was NOT re-judged on a cycle that never touched it — the re-fire did not happen")
	}

	// Cycle 3: FIX the marked file. Judge passes; it clears.
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
	e.Run(proj, sess, "make the mock conform", Turns("done",
		Write("w3", "internal/mock/trajectory.go", markedMock(docURL, "func Emit() string { return `{\"type\":\"assistant\"}` }")),
	))

	// Cycle 4: another unmarked file, judge armed to refuse. Nothing should re-fire.
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "R4 must not appear if the fix cleared the file"}`)
	e.Run(proj, sess, "write another unmarked helper", Turns("done",
		Write("w4", "internal/mock/helper2.go", "package mock\n\nfunc Helper2() {}\n"),
	))
	if hasReason(e, proj, sess, "R4") {
		t.Errorf("a FIXED marked file kept re-firing: cycle 4 touched nothing the guard matches, yet a fresh block appeared")
	}
}
