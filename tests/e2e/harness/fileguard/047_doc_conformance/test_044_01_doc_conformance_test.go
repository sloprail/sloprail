package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
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
	).ThenCommit("write the files"))

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
	).ThenCommit("write the files"))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if !harness.HasCap(t, harness.CapScopedToolRules) {
		// The example's judge is limited to `Bash(curl -sL https://…/*)`, a scoped rule this
		// harness cannot grant: sr-agent refuses the run rather than round the grant up, so
		// the judge is never launched and no prompt is rendered.
		if prompt != "" {
			t.Fatalf("a harness without scoped tool rules launched a judge whose grant is scoped:\n%s", prompt)
		}
		return
	}
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
	).ThenCommit("write the files"))

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
