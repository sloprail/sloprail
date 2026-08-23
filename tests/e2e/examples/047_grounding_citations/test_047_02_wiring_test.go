package e2e

// TODO(D3): drive verdict via a10n-claude-mock once a10n-cli#470 lands + new mock
// on PATH; today InstallJudgeClaudeCapturing supplies the verdict AND records the
// rendered prompt.
//
// PREPARE -> TEMPLATE WIRING for grounding-citations. The judge-tier behavioural
// tests (test_047_01) key on the stub's reasoning, which proves the verdict path
// but NOT that the shipped prepare (fetch-cited-source.sh) reached the judge
// TEMPLATE: the renderer treats an undefined variable as empty, so
// quote-approximates-source.md.j2's `{{ c.source_text }}` / `{{ c.quote }}` loop
// renders fine whether prepare produced real citations or an empty list. These
// tests close that: they capture the rendered prompt and assert the EXTRACTED
// source text — a value present only if prepare fetched it from the cited file AND
// the template interpolated it — is in the prompt, and that a DIFFERENT source
// yields a DIFFERENT prompt.

import (
	"path/filepath"
	"strings"
	"testing"
)

// T047_07: the prepared citation — the quote AND the fetched source_text — reaches
// the judge's prompt. fetch-cited-source.sh pulls the text at the cited range out
// of the source file and hands it to the template under additionalContext.citations;
// the quote is the doc's own bracket text. Both must appear in the rendered prompt,
// which is only possible if prepare ran and the template's `{% for c ... %}` loop
// interpolated them.
func TestT047_07_PreparedSourceTextReachesJudgePrompt(t *testing.T) {
	e := newEnv(t)
	proj := gcProject(t, e)

	// A distinctive line at the cited range, and a distinctive quote, so a match in
	// the prompt cannot be a coincidence of common words.
	const sourceLine = "ZZ_SOURCE the treaty was signed in the autumn of that year"
	const quote = "QQ_QUOTE the treaty was signed in autumn"
	e.WriteFile(proj, "src/data.txt", "alpha\n"+sourceLine+"\ngamma\n")
	absSrc := filepath.Join(proj, "src/data.txt")
	doc := "# Report\n\nIt is recorded that " + cite(quote, absSrc, 2, 2) + ".\n"

	// Capture the prompt; a passing verdict so the doc admits (the judge still ran).
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt",
		`{"pass": true, "reasoning": "the quote matches the cited source"}`)

	sess := "s-047-07"
	e.Run(proj, sess, "write a grounded report", Turns("done",
		Write("w1", "report.md", doc),
	))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran — no prompt was captured (did the citation resolve to reach the judge?)")
	}
	// The FETCHED source text: present only if fetch-cited-source.sh read the cited
	// range AND the template rendered c.source_text. This is the prepare -> template
	// wiring, proven directly.
	if !strings.Contains(prompt, sourceLine) {
		t.Fatalf("the fetched source_text did not reach the judge prompt — prepare/template wiring is broken:\n%s", prompt)
	}
	// The doc's own quote, rendered by c.quote alongside the source it is judged against.
	if !strings.Contains(prompt, quote) {
		t.Fatalf("the cited quote did not reach the judge prompt:\n%s", prompt)
	}
}

// T047_08: a DIFFERENT cited source yields a DIFFERENT prompt. Without this, a
// prompt that hard-coded the source text (or ignored prepare entirely) would still
// pass T047_07. Two runs citing two different lines must produce two prompts, each
// carrying its OWN source text and not the other's — which can only happen if the
// prompt is derived from the actual cited file each time.
func TestT047_08_DifferentSourceYieldsDifferentPrompt(t *testing.T) {
	runWithSource := func(tag, sourceLine string) string {
		e := newEnv(t)
		proj := gcProject(t, e)
		e.WriteFile(proj, "src/data.txt", "alpha\n"+sourceLine+"\ngamma\n")
		absSrc := filepath.Join(proj, "src/data.txt")
		doc := "# Report\n\nSee " + cite("a claim about "+tag, absSrc, 2, 2) + ".\n"
		e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)
		e.Run(proj, "s-047-08-"+tag, "write a report", Turns("done",
			Write("w1", "report.md", doc),
		))
		p := e.JudgePrompt(proj, "judge-prompt.txt")
		if p == "" {
			t.Fatalf("[%s] the judge never ran — no prompt captured", tag)
		}
		return p
	}

	const lineA = "AAA_ONLY the northern route was chosen"
	const lineB = "BBB_ONLY the southern route was chosen"
	promptA := runWithSource("A", lineA)
	promptB := runWithSource("B", lineB)

	if promptA == promptB {
		t.Fatalf("two different cited sources produced identical judge prompts — the prompt does not reflect the source")
	}
	// Each prompt carries its own source and not the other's — the source text is
	// genuinely fetched per-run, not a fixed string.
	if !strings.Contains(promptA, lineA) || strings.Contains(promptA, lineB) {
		t.Fatalf("prompt A did not carry ONLY source A's text:\n%s", promptA)
	}
	if !strings.Contains(promptB, lineB) || strings.Contains(promptB, lineA) {
		t.Fatalf("prompt B did not carry ONLY source B's text:\n%s", promptB)
	}
}
