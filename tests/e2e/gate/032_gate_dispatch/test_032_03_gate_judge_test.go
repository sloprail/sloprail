package e2e

import (
	"testing"
)

// This file covers a JUDGE check: prepare runs, feeds additionalContext into the
// rendered template, sr-agent asks the model (a fixed stub here), and the verdict
// blocks or admits. It is the whole judge path — template render, prepare wiring,
// sr-agent, verify script, verdict — with only the model's text replaced by a fixed
// answer, the same substitution the old-format judge tests make.

// judgeGate is a Stop gate whose check is a prepare + judge — the action-proof
// shape. prepare pulls a value out and hands it to the judge as additionalContext;
// the judge template reads it and the model rules.
const judgeGate = `on:
  - event: Stop
checks:
  - prepare: ./prepare.sh
    judge: ./judge.md.j2
`

// prepareScript emits an additionalContext object. It reads the CheckPayload on
// stdin (a GateCheckPayload) and returns a fixed fact — standing in for a real
// prepare that would resolve something out of the transcript.
const prepareScript = `#!/bin/sh
cat >/dev/null
printf '%s' '{"additionalContext":{"claim":"the agent said it filled the form","proof_present":false}}'
`

// judgeTemplate renders against the judge input: the standard payload spread flat
// plus additionalContext. It branches on the prepared fact, proving prepare's
// output reached the template under additionalContext.
const judgeTemplate = `# Judge whether the action was proven

The agent's claim: {{ additionalContext.claim }}

{% if additionalContext.proof_present %}
Proof was supplied — assess whether it is adequate.
{% else %}
NO PROOF was supplied for the claim. This must fail: an auditable action with no
proof is exactly what this rule catches.
{% endif %}
`

// T032_08: a judge check that returns a FAILING verdict blocks the turn, and its
// prepare fed additionalContext into the prompt.
//
// The stub `claude` writes {"pass": false, ...} to the file sr-agent named, so the
// verify script refuses and sr-agent exits non-zero — the judge's fail-closed
// verdict. The turn is blocked, and the reasoning reaches the record.
func TestT032_08_JudgeCheckBlocks(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "prove-action", judgeGate, map[string]string{
		"prepare.sh":  prepareScript,
		"judge.md.j2": judgeTemplate,
	})
	// The model's stand-in: a failing verdict with a reasoning the engine relays.
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "the action has no proof, so it cannot be audited"}`)

	e.Run(proj, "s-032-08", "do an action then stop", Turns("done",
		Write("w1", "note.txt", "filled the form"),
	))

	blocks := e.BlockingErrorsFrom(proj, "s-032-08", "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a judge check returning a failing verdict did not block the turn")
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	// The judge's reasoning reaches the agent.
	if !containsStr(joined, "no proof") {
		t.Errorf("the judge's reasoning did not reach the agent:\n%s", joined)
	}
	// The verdict landed as fail in the gates map.
	if status := e.GateState(proj, "s-032-08", "prove-action"); status != "fail" {
		t.Errorf("a blocking judge gate recorded verdict %q, want \"fail\"", status)
	}
}

// T032_09: a judge check that returns a PASSING verdict admits — the turn ends.
//
// The control for T032_08: without it a judge path that always refused (a broken
// verify script, say) would pass T032_08 while being broken. The stub writes
// {"pass": true}, the verify script accepts, sr-agent exits 0, and the gate admits.
func TestT032_09_JudgeCheckAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "prove-action", judgeGate, map[string]string{
		"prepare.sh":  prepareScript,
		"judge.md.j2": judgeTemplate,
	})
	commitGuards(t, proj) // keep the guard's own prepare.sh/judge.md.j2 out of the cycle diff
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-032-09", "do an action then stop", Turns("done",
		Write("w1", "note.txt", "filled the form"),
	))

	blocks := e.BlockingErrorsFrom(proj, "s-032-09", "Stop")
	if len(blocks) != 0 {
		t.Errorf("a judge check returning a passing verdict blocked the turn anyway:\n%v", blocks)
	}
	if status := e.GateState(proj, "s-032-09", "prove-action"); status != "pass" {
		t.Errorf("a passing judge gate recorded verdict %q, want \"pass\"", status)
	}
}
