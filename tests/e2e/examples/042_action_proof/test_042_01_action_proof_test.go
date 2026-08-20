package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The action-proof gate wakes on Stop. Its one check is prepare + judge:
// find-action-and-proof.sh reads the turn's trajectory for an auditable action (a
// `fill_form`/`download_file` tool_use) and the proof that should accompany it (a
// `screenshot` output), and hands the judge {action_taken, action, action_input,
// proof}; screenshot-shows-all-fields.md.j2 rules on whether the proof is real.
//
// The judge's model verdict is the fixed stub (InstallJudgeClaude), the same
// substitution the template tests T032_08/09 make — pass:false blocks the Stop,
// pass:true admits. What is NOT stubbed is the prepare: a real `sr-session
// trajectory normalize` reads the trajectory the mock streamed, so the ACTION the
// agent took drives the additionalContext the template renders. The mock does not
// implement `screenshot`, so a screenshot turn yields no artifact and prepare
// reports proof:null — the honest "the audit has nothing to check" state, which is
// the violation the rule exists to catch; a turn that takes no action reports
// action_taken:false and the judge passes trivially.
//
// The scenarios prove: an action the auditor accepts ADMITS (happy); an action
// with no proof BLOCKS at Stop and the judge's reasoning reaches the agent
// (violation); a turn that took no auditable action ADMITS (the gate does not
// demand proof of nothing); and the ACTION the agent took reaches the rendered
// template, captured and asserted to change with the action (prepare -> template
// wiring).

// aFillForm is a turn that fills a contact form — an auditable action the prepare
// recognises by tool name. Its input is what an audit would later check field by
// field.
func aFillForm(id string) harness.Turn {
	return harness.ToolUse(id, "fill_form", map[string]string{
		"name":  "Ada Lovelace",
		"email": "ada@example.com",
	})
}

// aDownloadInvoice is a turn that downloads an invoice — the other auditable
// action. A distinct action name and input, so a test can tell which one the
// prepare pulled into the template.
func aDownloadInvoice(id string) harness.Turn {
	return harness.ToolUse(id, "download_file", map[string]string{
		"url": "https://vendor.example/invoice-42.pdf",
	})
}

// aScreenshot is a turn that takes a screenshot — the proof artifact. The mock
// answers it with a not-implemented tool_result carrying no toolUseResult, so the
// prepare finds the screenshot CALL but no artifact to show; proof stays null and
// the judge (the stub) is what decides whether the proof suffices.
func aScreenshot(id string) harness.Turn {
	return harness.ToolUse(id, "screenshot", map[string]string{"target": "contact-form"})
}

// T042_01: a turn that took an auditable action the auditor ACCEPTS admits — the
// happy path.
//
// The turn fills a form and screenshots it; the prepare reports action_taken:true
// and the template renders the action for judging; the stub returns pass:true (the
// auditor confirmed the proof), the verify script accepts, sr-agent exits 0, and
// the Stop gate admits. This is the control every block below rests on: without it
// a gate that blocked every action would pass the violation tests while being
// broken.
func TestT042_01_AcceptedActionAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-042-01", "fill the form and prove it", Turns("done",
		aFillForm("w1"),
		aScreenshot("w2"),
	))

	if blocks := e.BlockingErrorsFrom(proj, "s-042-01", "Stop"); len(blocks) != 0 {
		t.Fatalf("an accepted-proof action was blocked anyway:\n%v", blocks)
	}
	if s := e.GateState(proj, "s-042-01", "screenshot-proves-fields"); s != "pass" {
		t.Errorf("an admitted action-proof gate recorded verdict %q, want \"pass\"", s)
	}
}

// T042_02: an auditable action with no proof BLOCKS at Stop, and the judge's
// reasoning reaches the agent — the violation this rule exists to catch.
//
// The turn downloads an invoice and stops with NO screenshot; the prepare reports
// action_taken:true, proof:null and the template takes its "No proof artifact was
// found. Fail" branch. The stub returns pass:false with the reasoning the rule
// would give; the verify script refuses, sr-agent exits non-zero, the Stop gate
// blocks and the words reach the agent.
func TestT042_02_ActionWithoutProofBlocks(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "the download has no screenshot proving the invoice fields were captured"}`)

	e.Run(proj, "s-042-02", "download the invoice then stop", Turns("done",
		aDownloadInvoice("w1"),
	))

	blocks := e.BlockingErrorsFrom(proj, "s-042-02", "Stop")
	if len(blocks) == 0 {
		t.Fatalf("an auditable action with no proof did not block the Stop gate")
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	if !containsStr(joined, "no screenshot proving") {
		t.Errorf("the judge's reasoning did not reach the agent:\n%s", joined)
	}
	if s := e.GateState(proj, "s-042-02", "screenshot-proves-fields"); s != "fail" {
		t.Errorf("the blocking action-proof gate recorded verdict %q, want \"fail\"", s)
	}
}

// T042_03: a turn that took NO auditable action admits — the gate does not demand
// proof of nothing.
//
// The does-not-fire-on-nothing control for a Stop gate: the gate always runs at
// Stop, but its prepare reports action_taken:false (the agent only ran a Bash), so
// the template's "No auditable action this turn ... Pass" branch renders and the
// judge passes trivially. The stub is set to pass:true — the verdict that branch
// calls for — and the turn admits. Proven to be the no-action path (not merely an
// admit) via the captured prompt: the template took its no-action branch, which
// renders only when prepare reported no action.
func TestT042_03_NoActionAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-042-03", "just run a command", Turns("done",
		harness.Bash("b1", "echo hello"),
	))

	if blocks := e.BlockingErrorsFrom(proj, "s-042-03", "Stop"); len(blocks) != 0 {
		t.Errorf("a turn with no auditable action was blocked by the proof gate:\n%v", blocks)
	}
	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran, so the no-action branch was not exercised")
	}
	if !containsStr(prompt, "No auditable action this turn") {
		t.Errorf("the template did not render its no-action branch — prepare's action_taken:false did not reach it:\n%s", prompt)
	}
	// And it must NOT have rendered the proof-demanding branch.
	if containsStr(prompt, "must carry proof an audit can check") {
		t.Errorf("the template demanded proof on a turn that took no action:\n%s", prompt)
	}
	if s := e.GateState(proj, "s-042-03", "screenshot-proves-fields"); s != "pass" {
		t.Errorf("a no-action Stop recorded gate verdict %q, want \"pass\"", s)
	}
}

// T042_04: the ACTION the agent took reaches the rendered template — the
// prepare -> template wiring, proven directly and shown to change with the action.
//
// A stubbed verdict cannot show this: the renderer treats an undefined variable as
// empty, so the template renders whether prepare produced the action or produced
// nothing. So the capturing shim records the prompt, and the test asserts the
// trajectory's OWN action name and input appear in it — a download_file of
// invoice-42.pdf renders "download_file" and the invoice URL and takes the
// no-proof branch; a fill_form of ada@example.com renders "fill_form" and that
// email; neither leaks the other. The value is present only if prepare read it off
// the trajectory AND the template interpolated it.
func TestT042_04_PreparedActionReachesTemplate(t *testing.T) {
	// Case 1: a download_file action, no screenshot.
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": false, "reasoning": "no proof"}`)

	e.Run(proj, "s-042-04a", "download the invoice", Turns("done",
		aDownloadInvoice("w1"),
	))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran for the download action")
	}
	if !containsStr(prompt, "download_file") {
		t.Errorf("the action name the agent took (download_file) did not reach the template:\n%s", prompt)
	}
	if !containsStr(prompt, "invoice-42.pdf") {
		t.Errorf("the action INPUT from the trajectory did not reach the template:\n%s", prompt)
	}
	// proof is null (no screenshot), so the no-proof branch renders — proving the
	// prepare's proof field reached the template too, not just the action.
	if !containsStr(prompt, "No proof artifact was found") {
		t.Errorf("the template did not take the no-proof branch for an action with no artifact:\n%s", prompt)
	}
	// The other action's fingerprint must not appear — this render is about THIS
	// trajectory, not a fixed string.
	if containsStr(prompt, "fill_form") || containsStr(prompt, "ada@example.com") {
		t.Errorf("the template leaked an action the agent did not take:\n%s", prompt)
	}

	// Case 2: a DIFFERENT action, a fresh session — the template must follow it.
	e2 := New(t)
	proj2 := e2.Project()
	e2.GitInit(proj2)
	installExampleTree(t, proj2)
	e2.InstallJudgeClaudeCapturing(proj2, "judge-prompt.txt", `{"pass": false, "reasoning": "no proof"}`)

	e2.Run(proj2, "s-042-04b", "fill the form", Turns("done",
		aFillForm("w1"),
	))

	prompt2 := e2.JudgePrompt(proj2, "judge-prompt.txt")
	if prompt2 == "" {
		t.Fatalf("the judge never ran for the fill_form action")
	}
	if !containsStr(prompt2, "fill_form") || !containsStr(prompt2, "ada@example.com") {
		t.Errorf("the fill_form action and its input did not reach the template:\n%s", prompt2)
	}
	if containsStr(prompt2, "download_file") || containsStr(prompt2, "invoice-42.pdf") {
		t.Errorf("the template still carried the previous run's action — the render is not following the trajectory:\n%s", prompt2)
	}
}
