package e2e

// TODO(D3): drive verdict via a10n-claude-mock once a10n-cli#470 lands + new mock
// on PATH; today InstallJudgeClaudeCapturing supplies the verdict AND records the
// rendered prompt.
//
// EVENT -> TEMPLATE WIRING for business-invariants. The judge renders the marked
// file from the CheckPayload's own event (code-upholds-invariant.md.j2:
// `{{ event.newContent if event.newContent else event.oldContent }}`) and the
// pinned spec text its prepare (pinned-text.sh) read at each marker's pin. A stub
// that only flips the verdict proves neither reached the prompt. This captures the
// rendered prompt and asserts the marked file's content — the sr:invariant
// marker's pinned fqn AND the code body — and the pinned spec line are in it, and
// that different content yields a different prompt.

import (
	"strings"
	"testing"
)

// T046_08: the marked file's content reaches the judge prompt. The pin matches HEAD
// (so the script passes to the judge), and the file carries a distinctive marker
// fqn and code body. Both must appear in the rendered prompt — only possible if the
// event's newContent reached the template.
func TestT046_08_EventContentReachesJudgePrompt(t *testing.T) {
	e := newEnv(t)
	proj := biProject(t, e)
	sha := commitSpec(t, e, proj, "SPEC.md", specV1, "spec v1")

	fqn := proj + "@" + sha + ":SPEC.md#L2-2"
	const body = "func charge(total int) { if total < 0 { panic(\"ZZ_GUARD never negative\") } }\n"
	code := invariantCode(fqn, body)

	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt",
		`{"pass": true, "reasoning": "the guard clause enforces never-negative"}`)

	sess := "s-046-08"
	e.Run(proj, sess, "add invariant-upholding code", Turns("done",
		Write("w1", "src/charge.go", code),
	).ThenCommit("write the files"))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran — no prompt captured (did the pin match HEAD to reach the judge?)")
	}
	// The marker's pinned fqn: present only if event.newContent (which carries the
	// marker line) reached the template.
	if !strings.Contains(prompt, fqn) {
		t.Fatalf("the marker's pinned fqn did not reach the judge prompt — event/template wiring is broken:\n%s", prompt)
	}
	// The distinctive code body the judge rules on.
	if !strings.Contains(prompt, "ZZ_GUARD never negative") {
		t.Fatalf("the marked code body did not reach the judge prompt:\n%s", prompt)
	}
	// The Fail section tells the judge to say what to do, not only what is wrong:
	// a real run bounced off Stop seven times on reasons that never said "undo it",
	// and another reshaped the feature when told to "bring it within the rule".
	if !strings.Contains(prompt, "Do not reshape the requested feature") || !strings.Contains(prompt, "The turn cannot end while") {
		t.Errorf("the judge is not asked to name the remedy:\n%s", prompt)
	}
	// "bring it within the rule" steered a real run (234432Z) into reshaping the
	// requested feature — moving the credit before the check — instead of raising
	// the conflict.
	// Two runs (003519Z, 003646Z) undid the bypass but left the goodwill flag
	// doing nothing and told the user a credit is "issued separately".
	if !strings.Contains(prompt, "do not leave a flag that\nchanges nothing") {
		t.Errorf("the judge is not asked to warn against an inert flag:\n%s", prompt)
	}
	if strings.Contains(prompt, "bring it within the rule") {
		t.Errorf("the judge still offers bringing the code within the rule as a remedy:\n%s", prompt)
	}
	// The pinned spec line, read by the prepare at the marker's pin, so the judge
	// rules on it without having to read the spec itself.
	if !strings.Contains(prompt, "<pinned file=\"src/charge.go\" fqn=\""+fqn+"\"") ||
		!strings.Contains(prompt, "an order total must never be negative\n</pinned>") {
		t.Fatalf("the pinned spec text did not reach the judge prompt — the prepare's output is not wired:\n%s", prompt)
	}
}

// T046_09: different marked content yields a different prompt. Without this, a
// prompt that ignored the event would still pass T046_08. Two runs with two
// different code bodies must produce two prompts, each carrying its OWN body.
func TestT046_09_DifferentContentYieldsDifferentPrompt(t *testing.T) {
	runWithBody := func(tag, body string) string {
		e := newEnv(t)
		proj := biProject(t, e)
		sha := commitSpec(t, e, proj, "SPEC.md", specV1, "spec v1")
		fqn := proj + "@" + sha + ":SPEC.md#L2-2"
		e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)
		e.Run(proj, "s-046-09-"+tag, "add code", Turns("done",
			Write("w1", "src/charge.go", invariantCode(fqn, body)),
		).ThenCommit("write the files"))
		p := e.JudgePrompt(proj, "judge-prompt.txt")
		if p == "" {
			t.Fatalf("[%s] the judge never ran — no prompt captured", tag)
		}
		return p
	}

	const bodyA = "func charge(int) { /* AAA_ONLY guard variant one */ }\n"
	const bodyB = "func charge(int) { /* BBB_ONLY guard variant two */ }\n"
	promptA := runWithBody("A", bodyA)
	promptB := runWithBody("B", bodyB)

	if promptA == promptB {
		t.Fatalf("two different marked bodies produced identical judge prompts — the prompt does not reflect the event content")
	}
	if !strings.Contains(promptA, "AAA_ONLY") || strings.Contains(promptA, "BBB_ONLY") {
		t.Fatalf("prompt A did not carry ONLY body A:\n%s", promptA)
	}
	if !strings.Contains(promptB, "BBB_ONLY") || strings.Contains(promptB, "AAA_ONLY") {
		t.Fatalf("prompt B did not carry ONLY body B:\n%s", promptB)
	}
}
