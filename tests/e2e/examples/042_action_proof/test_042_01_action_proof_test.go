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
// ---------------------------------------------------------------------------
// AN EXAMPLE BUG BLOCKS THE JUDGE PATH — see T042_03 for the pin and the proof.
//
// The prepare's jq iterates a message's content as an array:
//
//	[ .[] | (.message | objects | .content // [])[] | select(...) ]
//
// The `| objects` guard protects a non-object `.message`, but NOT a string
// `.content`. A user message whose content is a plain string — `{"role":"user",
// "content":"do the thing"}`, the ordinary shape of a typed prompt and the shape
// every Claude Code session's first user turn takes — makes `[]` fail with
// "Cannot iterate over string". The prepare then exits non-zero, and the gate's
// check refuses FAIL-CLOSED with the jq error before the model is ever asked.
//
// This is not a harness artifact: real transcripts carry string-content user
// messages (internal/transcript fixtures show `"content":"please refactor the
// parser"` alongside array-content turns), so the prepare breaks against real
// sessions the same way. The harness merely surfaces it, because it seeds a
// string-content root user entry the way a real session's first prompt is.
//
// The consequence for THIS use case: the intended judge pass path, the judge-fail
// path, and the prepare -> template wiring are all UNREACHABLE through the
// example as it ships — the prepare never yields clean additionalContext, so the
// template is never rendered and the stub verdict is never consulted. What CAN be
// proven, and is below, is the gate's binding (it wakes on Stop and runs the
// check) and its fail-closed discipline (a check whose prepare cannot run refuses
// rather than admitting). The judge-path scenarios are named and skipped, so the
// coverage they are waiting on is visible rather than silently absent.
//
// TODO(D3): drive the verdict via a10n-claude-mock once a10n-cli#470 lands and the
// new mock binary is on PATH; today the proven InstallJudgeClaude stub supplies
// the model verdict. Independently, once the example prepare guards its content
// iteration (see the spawned task) the SKIPPED judge-path scenarios below become
// runnable — the happy admit, the no-proof fail with reasoning reaching the agent,
// and the prepare -> template wiring via the captured prompt.
// ---------------------------------------------------------------------------

// aFillForm is a turn that fills a contact form — an auditable action the prepare
// recognises by tool name.
func aFillForm(id string) harness.Turn {
	return harness.ToolUse(id, "fill_form", map[string]string{
		"name":  "Ada Lovelace",
		"email": "ada@example.com",
	})
}

// aScreenshot is a turn that takes a screenshot — the proof artifact. The mock
// answers it with a not-implemented tool_result carrying no toolUseResult, so even
// once the prepare runs cleanly the screenshot yields no artifact and proof stays
// null (the honest "the audit has nothing to check" state).
func aScreenshot(id string) harness.Turn {
	return harness.ToolUse(id, "screenshot", map[string]string{"target": "contact-form"})
}

// T042_01: the action-proof gate wakes on Stop and RUNS its check — the binding.
//
// The agent takes an auditable action and stops. The gate fires at Stop; its check
// runs. Because of the example's prepare bug the check refuses fail-closed with
// the prepare's own error rather than reaching the judge — so this asserts the
// gate blocked at Stop and recorded a fail, which is the binding plus the
// fail-closed discipline. It deliberately does NOT assert a judge reasoning, which
// today's example cannot produce; that is T042_04's skipped concern.
func TestT042_01_GateFiresOnStopAndRunsCheck(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-042-01", "fill the form and screenshot it", Turns("done",
		aFillForm("w1"),
		aScreenshot("w2"),
	))

	blocks := e.BlockingErrorsFrom(proj, "s-042-01", "Stop")
	if len(blocks) == 0 {
		t.Fatalf("the action-proof gate did not fire/block at Stop on a turn that took an auditable action")
	}
	if s := e.GateState(proj, "s-042-01", "screenshot-proves-fields"); s != "fail" {
		t.Errorf("the action-proof gate recorded verdict %q at Stop, want \"fail\" (fail-closed)", s)
	}
}

// T042_02: the check that could not run FAILS CLOSED — the refusal reaches the
// agent and names the check's own failure, never a silent admit.
//
// A gate whose check cannot be evaluated must refuse: "a check that could not be
// checked is not a check that passed." Here the prepare crashes on the string
// content of the session's first user message, and the engine surfaces the
// prepare's failure to the agent rather than letting the stop through. The stub is
// set to pass:true precisely so that a bug which let the gate ADMIT on a prepare
// error would be caught here — the admit would win and this would fail.
func TestT042_02_PrepareFailureFailsClosed(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	// A passing verdict — so if the engine ever reached the (unreachable) judge, or
	// if it wrongly admitted on a prepare error, the turn would NOT block. It does
	// block, which is the fail-closed property.
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-042-02", "fill the form", Turns("done",
		aFillForm("w1"),
	))

	blocks := e.BlockingErrorsFrom(proj, "s-042-02", "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a gate whose prepare could not run admitted the stop — it must fail closed")
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	// The refusal names that it was the PREPARE step that could not run, so the
	// failure is diagnosable rather than a bare block.
	if !containsStr(joined, "prepare") {
		t.Errorf("the fail-closed refusal did not name the prepare step as the cause:\n%s", joined)
	}
}

// T042_03: PIN — the example's prepare bug is exactly the string-content-message
// crash, surfaced verbatim to the agent.
//
// This is the evidence for the block above: it asserts the refusal carries the
// jq "Cannot iterate over string" error, proving the fail-closed refusal is the
// prepare crashing on a plain-string user message — the specific example bug — and
// not some unrelated refusal. When the example is fixed this test will start
// failing (the crash text will be gone), which is the correct signal to switch the
// skipped judge-path scenarios on.
func TestT042_03_PreparePinsTheStringContentBug(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-042-03", "download the invoice", Turns("done",
		harness.ToolUse("w1", "download_file", map[string]string{"url": "https://vendor.example/invoice-42.pdf"}),
	))

	joined := ""
	for _, b := range e.BlockingErrorsFrom(proj, "s-042-03", "Stop") {
		joined += b + "\n"
	}
	if !containsStr(joined, "Cannot iterate over string") {
		t.Fatalf("expected the prepare to crash on the string-content user message (the example bug); "+
			"if this text is gone the example was fixed and the skipped judge-path scenarios should be enabled:\n%s", joined)
	}
}

// T042_04: SKIPPED until the example prepare is fixed — the JUDGE-PATH coverage.
//
// These are the scenarios a working action-proof needs, spelled out so the gap is
// visible: (happy) an action whose proof the auditor accepts ADMITS; (violation)
// an action with no proof BLOCKS and the judge's reasoning reaches the agent;
// (wiring) the trajectory's own action name and input reach the rendered template,
// captured via InstallJudgeClaudeCapturing and asserted to change with the action.
// All are blocked on the prepare producing clean additionalContext, which it
// cannot while it crashes on string-content messages (T042_03). Named and skipped
// rather than omitted, so the coverage this use case is waiting on is not silent.
func TestT042_04_JudgePathBlockedOnExampleBug(t *testing.T) {
	t.Skip("action-proof's judge path is unreachable until examples/action-proof's prepare guards its " +
		"content iteration against string-content user messages (see T042_03 and the spawned fix task). " +
		"Once fixed, exercise: happy admit (stub pass:true), no-proof fail with the judge reasoning reaching " +
		"the agent (stub pass:false), and prepare->template wiring via JudgePrompt showing the trajectory's " +
		"action name/input — with a fresh-session control proving the render follows the action, not a fixed string.")
}
