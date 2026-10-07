package e2e

import (
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
