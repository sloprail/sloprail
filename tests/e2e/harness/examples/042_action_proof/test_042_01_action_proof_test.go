package e2e

import (
	"strconv"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// TODO(D3): drive the verdict via a10n-claude-mock once a10n-cli#470's mock grows
// sr-agent's claude-flag surface for this path; today the proven InstallJudgeClaude
// stub supplies the model verdict (the same substitution T032_08/09 make).
//
// The action-proof gate wakes on Stop. Its one check is prepare + judge:
// find-action-and-proof.sh reads the turn's trajectory for an auditable action (a
// `fill_form`/`download_file` tool_use) and the proof that should accompany it (a
// `screenshot` whose `toolUseResult` an audit reads back), and hands the judge
// {action_taken, action, action_input, proof}; screenshot-shows-all-fields.md.j2
// rules on whether the proof is real.
//
// The judge's model verdict is the fixed stub (InstallJudgeClaude) — pass:false
// blocks the Stop, pass:true admits. What is NOT stubbed is the prepare: a real
// `sr-session trajectory normalize` reads the trajectory the mock streamed, so the
// ACTION the agent took and whether a proof artifact accompanies it drive the
// additionalContext the template renders. The proof artifact is a screenshot's
// `toolUseResult`, supplied via the ToolUseWithResult builder (the mock's own
// synthesised result carries none); a turn that takes an action with NO such
// artifact makes the prepare report proof:null and the template render its
// "No proof artifact was found. Fail" branch — the genuine violation.
//
// The scenarios prove, each non-vacuously: an action WITH a real proof artifact
// ADMITS and the template rendered the proof-PRESENT branch (not the no-proof one)
// — so admit is earned by the proof, not merely by the stub; an action with NO
// proof BLOCKS and the template rendered the no-proof branch and the judge's
// reasoning reached the agent; a turn that took no auditable action ADMITS; and the
// ACTION reaches the rendered template and changes with the trajectory.

// aFillForm is a turn that fills a contact form — an auditable action the prepare
// recognises by tool name. Its input is what an audit would later check field by
// field.
func aFillForm(id string) harness.Turn {
	return harness.ToolUseJSON(id, "mcp__browser__fill_form", `{"name":"Ada Lovelace","email":"ada@example.com",`+
		`"mock_result":{"content":[{"type":"text","text":"Filled 2 fields"}],"isError":false}}`)
}

// aDownloadInvoice is a turn that downloads an invoice — the other auditable
// action. A distinct action name and input, so a test can tell which one the
// prepare pulled into the template.
func aDownloadInvoice(id string) harness.Turn {
	return harness.ToolUseJSON(id, "mcp__browser__download_file", `{"url":"https://vendor.example/invoice-42.pdf",`+
		`"mock_result":{"content":[{"type":"text","text":"Saved the download"}],"isError":false}}`)
}

// screenshotProof is the text a screenshot tool returned: the mock does not model image results,
// and an MCP tool may answer in text, so the proof is a description an auditor reads. Distinctive
// text so a test can find it in the rendered prompt (proving the prepare pulled the tool's result
// into the template).
const screenshotProof = "screenshot of the filled contact form showing name=Ada Lovelace and email=ada@example.com, every field visible"

// aScreenshotWithProof is the turn for a screenshot that PRODUCED a proof artifact: an MCP call whose
// result (mock_result) is the text above. The prepare correlates the call with its result by
// tool-use id and reports proof non-null.
func aScreenshotWithProof(id string) harness.Turn {
	return harness.ToolUseJSON(id, "mcp__browser__screenshot", `{"target":"contact-form",`+
		`"mock_result":{"content":[{"type":"text","text":`+strconv.Quote(screenshotProof)+`}],"isError":false}}`)
}

// T042_01: an action WITH a real proof artifact ADMITS, and the template rendered
// the proof-PRESENT branch — so the admit is earned by the proof, not the stub.
//
// The turn fills a form and takes a screenshot whose toolUseResult carries the
// proof; the prepare reports proof non-null and the template renders the
// proof-present material (NOT the "No proof artifact was found" branch). The stub
// returns pass:true (the auditor confirmed the proof), so the gate admits. The
// capturing shim proves the proof reached the prompt AND the no-proof branch did
// not render — which is what makes this distinct from "stub pass:true ⇒ admit":
// the proof-present path is genuinely exercised.
func TestT042_01_ProvenActionAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-042-01", "fill the form and prove it with a screenshot", Turns("done",
		aFillForm("w1"),
		aScreenshotWithProof("w2"),
	))

	if blocks := e.BlockingErrorsFrom(proj, "s-042-01", "Stop"); len(blocks) != 0 {
		t.Fatalf("a proven action was blocked anyway:\n%v", blocks)
	}
	if s := e.GateState(proj, "s-042-01", "screenshot-proves-fields"); s != "pass" {
		t.Errorf("an admitted action-proof gate recorded verdict %q, want \"pass\"", s)
	}
	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran, so the proof-present path was not exercised")
	}
	// The proof artifact reached the template — the prepare pulled the screenshot's
	// toolUseResult in.
	if !containsStr(prompt, "screenshot of the filled contact form") {
		t.Errorf("the proof artifact did not reach the template — proof was not carried through:\n%s", prompt)
	}
	// And the no-proof branch did NOT render — this is the proof-PRESENT path, the
	// branch a null-proof trajectory can never reach.
	if containsStr(prompt, "No proof artifact was found") {
		t.Errorf("the template rendered the no-proof branch despite a proof artifact being present:\n%s", prompt)
	}
}

// T042_02: an auditable action with no proof BLOCKS at Stop, the template rendered
// the no-proof branch, and the judge's reasoning reaches the agent — the violation.
//
// The turn downloads an invoice and stops with NO screenshot; the prepare reports
// action_taken:true, proof:null and the template takes its "No proof artifact was
// found. Fail" branch. The stub returns pass:false with the reasoning the rule
// would give; the gate blocks and the words reach the agent. The captured prompt
// proves the no-proof branch genuinely rendered (not merely that the stub failed).
func TestT042_02_ActionWithoutProofBlocks(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": false, "reasoning": "the download has no screenshot proving the invoice fields were captured"}`)

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
	// The template genuinely rendered the no-proof branch — the proof:null the
	// prepare produced reached it.
	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran, so the no-proof branch was not exercised")
	}
	if !containsStr(prompt, "No proof artifact was found") {
		t.Errorf("the template did not render the no-proof branch on an action with no proof:\n%s", prompt)
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
// admit) via the captured prompt.
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
	if !containsStr(prompt, "No proof artifact was found") {
		t.Errorf("the template did not take the no-proof branch for an action with no artifact:\n%s", prompt)
	}
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

// T042_05: an action's structured input and a structured proof reach the judge
// as JSON, every value readable.
//
// The judge is asked to check the values the agent supplied against the proof,
// so a number, a nested object or a content-block array must reach it as the
// value, not as a Go placeholder (`<float64 Value>`, `<map[string]interface {}
// Value>`) — which is what printing a map straight into the template gives.
func TestT042_05_StructuredInputAndProofReachJudgeAsJSON(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-042-05", "fill the form and prove it with a screenshot", Turns("done",
		harness.ToolUseJSON("w1", "mcp__browser__fill_form", `{"name":"Ada","age":36,"address":{"city":"London"},"tags":["vip"],`+
			`"mock_result":{"content":[{"type":"text","text":"Filled 4 fields"}],"isError":false}}`),
		// The proof is the MCP result's content blocks: image results are not modelled, so the
		// screenshot answers in two text blocks (an MCP tool may), which reach the judge as a JSON array.
		harness.ToolUseJSON("w2", "mcp__browser__screenshot", `{"target":"contact-form",`+
			`"mock_result":{"content":[{"type":"text","text":"form screenshot, 1280px wide"},{"type":"text","text":"name=Ada age=36 city=London"}],"isError":false}}`),
	))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran for the structured fill_form action")
	}
	for _, want := range []string{`"age":36`, `"city":"London"`, `"tags":["vip"]`, `"text":"form screenshot, 1280px wide"`, `"text":"name=Ada age=36 city=London"`, `"type":"text"`} {
		if !containsStr(prompt, want) {
			t.Errorf("the judge prompt does not carry %s as JSON:\n%s", want, prompt)
		}
	}
	if containsStr(prompt, "interface {} Value") || containsStr(prompt, "float64 Value") || containsStr(prompt, "[]interface") {
		t.Errorf("a structured value reached the judge as a Go placeholder, not its value:\n%s", prompt)
	}
}
