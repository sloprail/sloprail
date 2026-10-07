package e2e

import "testing"

// T046_15: a doc example of the marker inside a skill is not a pin. The scanner
// reads `// sr:invariant "<...>"` anywhere in a file's text, so a SKILL.md that
// teaches the marker used to be judged as code carrying a (placeholder) pin and
// refused. The rule is about code, so a file under .claude/ is out of its match.
// The control, a real code file carrying the same placeholder pin, is still refused
// by the same rule in the same session — the narrowing loosens nothing for code.
func TestT046_15_SkillDocExampleIsNotAPin(t *testing.T) {
	e := newEnv(t)
	proj := biProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the script refuses first"}`)

	commitSpec(t, e, proj, "SPEC.md", specV1, "seed")
	settleBaseline(t, e, proj, "s-046-15", "settle the installed example")

	example := "---\nname: pin-invariants\ndescription: pin code to a spec line\n---\n\n" +
		"Mark the code:\n\n    // sr:invariant \"<the pin>\"\n"

	// The doc example inside a skill is admitted (first, so a refusal here is its own: a
	// refused range is replayed until the commits change).
	e.Run(proj, "s-046-15b", "document the marker", Turns("done",
		Write("w1", ".claude/skills/pin-invariants/SKILL.md", example),
	).ThenCommit("document the marker"))
	if blocks := e.BlockingErrorsFrom(proj, "s-046-15b", "Stop"); len(blocks) != 0 {
		t.Fatalf("a SKILL.md doc example of the marker was judged as a pin:\n%s", joinBlocks(blocks))
	}

	// Control: the same marker in real code is refused (the pin does not resolve).
	e.Run(proj, "s-046-15a", "add pinned code", Turns("done",
		Write("w1", "src/charge.go", "// sr:invariant \"<the pin>\"\nfunc charge() {}\n"),
	).ThenCommit("write the code"))
	if blocks := e.BlockingErrorsFrom(proj, "s-046-15a", "Stop"); len(blocks) == 0 {
		t.Fatalf("a real code file carrying a placeholder pin was not refused")
	}
}
